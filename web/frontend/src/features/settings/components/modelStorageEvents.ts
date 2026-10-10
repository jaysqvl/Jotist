// The stream carries invalidations only. It never polls inventory or transmits
// paths, recording details, model parameters or credentials in event payloads.
export function watchModelStorage(
    token: string,
    onChange: () => void,
    onConnection: (connected: boolean) => void,
    fetcher: typeof fetch = fetch,
    inactivityMs = 90000,
): () => void {
    const controller = new AbortController();
    let retry: ReturnType<typeof setTimeout> | undefined;
    let refresh: ReturnType<typeof setTimeout> | undefined;
    let failures = 0;
    const changed = () => {
        if (refresh || controller.signal.aborted) return;
        // Coalesce adjacent preparation, queue and stage notifications.
        refresh = setTimeout(() => {
            refresh = undefined;
            if (!controller.signal.aborted) onChange();
        }, 200);
    };
    const connect = async () => {
        let reader: ReadableStreamDefaultReader<Uint8Array> | undefined;
        let retryAllowed = true;
        const request = new AbortController();
        const abortRequest = () => request.abort();
        controller.signal.addEventListener('abort', abortRequest, { once: true });
        let inactivity: ReturnType<typeof setTimeout> | undefined;
        const received = () => {
            clearTimeout(inactivity);
            // The server sends a transport keepalive every 30 seconds. Recover
            // stalled proxies/connections after three missed keepalives. This
            // reconnects the stream only; it never schedules inventory polling.
            inactivity = setTimeout(abortRequest, inactivityMs);
        };
        try {
            received();
            const response = await fetcher('/api/v1/settings/models/events', {
                headers: { Authorization: `Bearer ${token}` }, signal: request.signal,
            });
            if (response.status === 401 || response.status === 403) retryAllowed = false;
            if (!response.ok || !response.body) throw new Error('Model storage events unavailable');
            reader = response.body.getReader();
            const decoder = new TextDecoder();
            const parser = modelStorageEventParser(type => {
                if (controller.signal.aborted) return;
                if (type === 'connected') {
                    failures = 0;
                    onConnection(true);
                    // Reconcile events missed before connection or reconnect.
                    changed();
                } else if (type === 'model_storage_changed') changed();
            });
            while (!controller.signal.aborted) {
                const { done, value } = await reader.read();
                if (done) break;
                received();
                parser(decoder.decode(value, { stream: true }));
            }
        } catch {
            // Reconnect below, including normal EOF. Aborts never reconnect.
        } finally {
            clearTimeout(inactivity);
            controller.signal.removeEventListener('abort', abortRequest);
            reader?.releaseLock();
            if (!controller.signal.aborted) {
                onConnection(false);
                if (retryAllowed) {
                    const delay = Math.min(30000, 1000 * 2 ** Math.min(failures++, 5));
                    retry = setTimeout(() => { void connect(); }, delay);
                }
            }
        }
    };
    void connect();
    return () => {
        controller.abort();
        clearTimeout(retry);
        clearTimeout(refresh);
    };
}

export function modelStorageEventParser(onEvent: (type: string) => void): (chunk: string) => void {
    let buffer = '';
    return chunk => {
        buffer += chunk;
        const frames = buffer.split(/\r?\n\r?\n/);
        buffer = frames.pop() ?? '';
        if (buffer.length > 65536) throw new Error('Oversized model storage event');
        for (const frame of frames) {
            const data = frame.split(/\r?\n/).filter(line => line.startsWith('data:'))
                .map(line => line.slice(5).trimStart()).join('\n');
            if (!data) continue;
            try {
                const event: unknown = JSON.parse(data);
                if (event && typeof event === 'object' && 'type' in event && typeof event.type === 'string') onEvent(event.type);
            } catch { /* Ignore malformed events and transport keepalives. */ }
        }
    };
}
