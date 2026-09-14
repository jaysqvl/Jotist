import assert from 'node:assert/strict';
import { beforeEach, test } from 'node:test';

class MemoryStorage implements Storage {
    private values = new Map<string, string>();
    get length() { return this.values.size; }
    clear() { this.values.clear(); }
    getItem(key: string) { return this.values.get(key) ?? null; }
    key(index: number) { return [...this.values.keys()][index] ?? null; }
    removeItem(key: string) { this.values.delete(key); }
    setItem(key: string, value: string) { this.values.set(key, value); }
}

Object.defineProperty(globalThis, 'localStorage', { value: new MemoryStorage(), configurable: true });
const { useAuthStore } = await import('../features/auth/store/authStore.ts');
const { setupAuthInterceptor } = await import('./authInterceptor.ts');
const { refreshToken, isTokenExpiring } = await import('./authHelpers.ts');

function installBrowser(fetch: typeof window.fetch) {
    Object.defineProperty(globalThis, 'window', {
        value: { fetch, location: new URL('https://jotist.example/') },
        configurable: true,
    });
    setupAuthInterceptor();
}

function effectiveRequest(input: RequestInfo | URL, init?: RequestInit): Request {
    return new Request(input instanceof Request ? input : new URL(input, window.location.href), init);
}

beforeEach(() => {
    localStorage.clear();
    useAuthStore.setState({ token: 'original-token', requiresRegistration: false, isInitialized: true });
});

test('auth transport leaves external, static and auth requests untouched', async () => {
    const calls: Array<{ input: RequestInfo | URL; init?: RequestInit }> = [];
    installBrowser(async (input, init) => {
        calls.push({ input, init });
        return new Response(null, { status: 401 });
    });

    for (const url of [
        'https://another.example/api/v1/transcription',
        '//another.example/api/v1/transcription',
        '/favicon.svg',
        '/api/v1/auth/login',
        '/api/v10/transcription',
    ]) {
        const response = await window.fetch(url);
        assert.equal(response.status, 401);
        assert.deepEqual(calls.at(-1), { input: url, init: undefined });
    }
    assert.equal(calls.length, 5);
    assert.equal(useAuthStore.getState().token, 'original-token');
});

test('a 401 retry preserves Request body and headers and uses the refreshed token', async () => {
    const received: Array<{ body: string; authorization: string | null; contentType: string | null; trace: string | null }> = [];
    let refreshes = 0;
    installBrowser(async (input, init) => {
        if (input === '/api/v1/auth/refresh') {
            refreshes += 1;
            return Response.json({ token: 'renewed-token' });
        }
        const request = effectiveRequest(input, init);
        received.push({
            body: await request.text(),
            authorization: request.headers.get('Authorization'),
            contentType: request.headers.get('Content-Type'),
            trace: request.headers.get('X-Trace'),
        });
        return new Response(null, { status: received.length === 1 ? 401 : 200 });
    });

    const response = await window.fetch(new Request('https://jotist.example/api/v1/transcription', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Trace': 'upload-42' },
        body: '{"title":"meeting"}',
    }));
    assert.equal(response.status, 200);
    assert.equal(refreshes, 1);
    assert.deepEqual(received, ['original-token', 'renewed-token'].map(token => ({
        body: '{"title":"meeting"}',
        authorization: `Bearer ${token}`,
        contentType: 'application/json',
        trace: 'upload-42',
    })));
});

test('concurrent unauthorized API requests share one refresh', async () => {
    let refreshes = 0;
    installBrowser(async (input, init) => {
        if (input === '/api/v1/auth/refresh') {
            refreshes += 1;
            return Response.json({ token: 'renewed-token' });
        }
        const request = effectiveRequest(input, init);
        return new Response(null, {
            status: request.headers.get('Authorization') === 'Bearer renewed-token' ? 200 : 401,
        });
    });
    const responses = await Promise.all([
        window.fetch('/api/v1/transcription/list'),
        window.fetch('/api/v1/user/settings'),
        window.fetch('/api/v1/transcription/models'),
    ]);
    assert.deepEqual(responses.map(response => response.status), [200, 200, 200]);
    assert.equal(refreshes, 1);
});

test('URL uploads retry with the original Blob instead of buffering a cloned stream', async () => {
    const upload = new Blob(['recorded audio'], { type: 'audio/webm' });
    let attempts = 0;
    installBrowser(async (input, init) => {
        if (input === '/api/v1/auth/refresh') return Response.json({ token: 'renewed-token' });
        assert.equal(input, '/api/v1/transcription/upload');
        assert.equal(init?.body, upload);
        assert.equal(await upload.text(), 'recorded audio');
        attempts += 1;
        return new Response(null, { status: attempts === 1 ? 401 : 200 });
    });
    const response = await window.fetch('/api/v1/transcription/upload', { method: 'POST', body: upload });
    assert.equal(response.status, 200);
    assert.equal(attempts, 2);
});

test('caller-provided upload streams are passed through without automatic replay', async () => {
    const upload = new ReadableStream();
    let attempts = 0;
    installBrowser(async (input, init) => {
        assert.equal(input, '/api/v1/transcription/upload');
        assert.equal(init?.body, upload);
        attempts += 1;
        return new Response(null, { status: 401 });
    });
    const response = await window.fetch('/api/v1/transcription/upload', { method: 'POST', body: upload });
    assert.equal(response.status, 401);
    assert.equal(attempts, 1);
    assert.equal(upload.locked, false);
    assert.equal(useAuthStore.getState().token, 'original-token');
});

test('a pending refresh cannot restore a logged-out session or overwrite a new login', async () => {
    for (const replacement of [null, 'new-login-token']) {
        useAuthStore.getState().setToken('original-token');
        let finishRefresh!: (response: Response) => void;
        installBrowser(() => new Promise(resolve => { finishRefresh = resolve; }));
        const pending = refreshToken();
        useAuthStore.getState().setToken(replacement);
        finishRefresh(Response.json({ token: 'stale-refreshed-token' }));
        assert.equal(await pending, replacement);
        assert.equal(useAuthStore.getState().token, replacement);
    }
});

test('explicit caller credentials are neither overwritten nor retried', async () => {
    let calls = 0;
    installBrowser(async (input, init) => {
        calls += 1;
        assert.equal(effectiveRequest(input, init).headers.get('Authorization'), 'Basic caller-credentials');
        return new Response(null, { status: 401 });
    });
    await window.fetch('/api/v1/transcription/list', { headers: { Authorization: 'Basic caller-credentials' } });
    assert.equal(calls, 1);
    assert.equal(useAuthStore.getState().token, 'original-token');
});

test('a failed refresh logs out the current session', async () => {
    installBrowser(async () => new Response(null, { status: 401 }));
    assert.equal((await window.fetch('/api/v1/transcription/list')).status, 401);
    assert.equal(useAuthStore.getState().token, null);
});

test('JWT refresh threshold accepts base64url and treats malformed expiry as expired', () => {
    const token = (payload: object) => `header.${Buffer.from(JSON.stringify(payload)).toString('base64url')}.signature`;
    assert.equal(isTokenExpiring(token({ exp: 1_301 }), 1_000_000), false);
    assert.equal(isTokenExpiring(token({ exp: 1_300 }), 1_000_000), true);
    assert.equal(isTokenExpiring(token({ exp: 'later' }), 1_000_000), true);
    assert.equal(isTokenExpiring('malformed', 1_000_000), true);
});
