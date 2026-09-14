import { useAuthStore } from '../features/auth/store/authStore.ts';
import './authTypes.ts';

let pendingRefresh: { token: string | null; promise: Promise<string | null> } | null = null;

export function refreshToken(): Promise<string | null> {
    const token = useAuthStore.getState().token;
    if (pendingRefresh?.token === token) return pendingRefresh.promise;

    const promise = requestRefresh(token).finally(() => {
        if (pendingRefresh?.promise === promise) pendingRefresh = null;
    });
    pendingRefresh = { token, promise };
    return promise;
}

async function requestRefresh(startingToken: string | null): Promise<string | null> {
    const originalFetch = window.__scriberr_original_fetch || window.fetch;

    try {
        const response = await originalFetch('/api/v1/auth/refresh', { method: 'POST' });
        if (!response.ok) return null;

        const data = await response.json();
        const state = useAuthStore.getState();
        // A pending refresh must not restore a logged-out or replaced session.
        if (state.token !== startingToken) return state.token;
        if (typeof data?.token === 'string' && data.token) {
            state.setToken(data.token);
            state.setRequiresRegistration(false);
            return data.token;
        }
        return null;
    } catch {
        return null;
    }
}

export function isTokenExpiring(token: string, now = Date.now()): boolean {
    try {
        const payload = JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')));
        return typeof payload.exp !== 'number' || payload.exp <= now / 1000 + 300;
    } catch {
        return true;
    }
}

export function navigateToHome(): void {
    if (window.location.pathname !== "/") {
        window.history.pushState({ route: { path: 'home' } }, "", "/");
        window.dispatchEvent(new PopStateEvent('popstate', { state: { route: { path: 'home' } } }));
    }
}

export function parseRequestUrl(input: RequestInfo | URL): string {
    if (typeof input === 'string') return input;
    if (input instanceof URL) return input.href;
    return input.url;
}
