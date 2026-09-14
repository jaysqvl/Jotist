import { useEffect, type ReactNode } from 'react';
import { isTokenExpiring, refreshToken } from '@/lib/authHelpers';
import { useAuth } from '../hooks/useAuth';
import { useAuthStore } from '../store/authStore';

// Session initialization and renewal belong to the application lifetime, not
// to every component that needs an auth header.
export function AuthProvider({ children }: { children: ReactNode }) {
    const { token, logout } = useAuth();

    useEffect(() => {
        if (useAuthStore.getState().isInitialized) return;
        const controller = new AbortController();

        async function initialize() {
            try {
                const response = await fetch('/api/v1/auth/registration-status', {
                    signal: controller.signal,
                });
                if (!response.ok) throw new Error('Could not load registration status');
                const data = await response.json();
                if (controller.signal.aborted) return;
                useAuthStore.getState().setRequiresRegistration(
                    typeof data.registration_enabled === 'boolean'
                        ? data.registration_enabled
                        : !!data.requiresRegistration,
                );
            } catch (error) {
                if (!controller.signal.aborted) console.error('Failed to initialize authentication', error);
            } finally {
                if (!controller.signal.aborted) useAuthStore.getState().setInitialized(true);
            }
        }

        void initialize();
        return () => controller.abort();
    }, []);

    useEffect(() => {
        if (!token) return;
        let disposed = false;

        async function checkExpiry() {
            if (!token || !isTokenExpiring(token)) return;
            const refreshed = await refreshToken();
            if (!disposed && !refreshed && useAuthStore.getState().token === token) logout();
        }

        void checkExpiry();
        const interval = window.setInterval(checkExpiry, 60_000);
        return () => {
            disposed = true;
            window.clearInterval(interval);
        };
    }, [token, logout]);

    return children;
}
