import { useAuthStore } from '../features/auth/store/authStore.ts';
import { refreshToken, navigateToHome, parseRequestUrl } from './authHelpers.ts';
import './authTypes.ts';

export function setupAuthInterceptor(): void {
    if (window.__scriberr_original_fetch) {
        return;
    }

    const originalFetch = window.fetch.bind(window);
    window.__scriberr_original_fetch = originalFetch;

    const wrappedFetch: typeof window.fetch = async (input, init) => {
        const url = new URL(parseRequestUrl(input), window.location.href);
        const isAppAPI = url.origin === window.location.origin && url.pathname.startsWith('/api/v1/');
        const isAuthEndpoint = url.pathname.startsWith('/api/v1/auth/');
        if (!isAppAPI || isAuthEndpoint) return originalFetch(input, init);

        const token = useAuthStore.getState().token;
        const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
        const authorization = headers.get('Authorization');
        // Explicit credentials owned by another caller must not be replaced.
        if (authorization && authorization !== `Bearer ${token}`) return originalFetch(input, init);
        if (token) headers.set('Authorization', `Bearer ${token}`);
        const requestInit = { ...init, headers };

        // URL + Blob/FormData/string requests can reuse their original body.
        // Only caller-owned Request bodies need a clone before consumption.
        // A caller-provided stream cannot be replayed without buffering it.
        const replayable = !(init?.body instanceof ReadableStream);
        const retryInput = replayable && input instanceof Request && input.body && !init?.body
            ? input.clone()
            : input;
        const signal = init?.signal ?? (input instanceof Request ? input.signal : undefined);
        let response = await originalFetch(input, requestInit);

        if (response.status === 401 && replayable && !signal?.aborted) {
            const currentToken = useAuthStore.getState().token;
            // Another request may already have refreshed this session.
            const newToken = currentToken !== token ? currentToken : await refreshToken();

            if (newToken) {
                const retryHeaders = new Headers(headers);
                retryHeaders.set('Authorization', `Bearer ${newToken}`);
                response = await originalFetch(retryInput, { ...init, headers: retryHeaders });
                if (response.status !== 401) return response;
            }

            if (useAuthStore.getState().token === (newToken ?? token)) {
                useAuthStore.getState().logout();
                navigateToHome();
            }
        }

        return response;
    };

    window.fetch = wrappedFetch;
}
