import { useCallback } from 'react';
import { useAuthStore } from '../store/authStore';
import { navigateToHome } from '../../../lib/authHelpers';

export function useAuth() {
    const {
        token,
        requiresRegistration,
        isInitialized,
        setToken,
        setRequiresRegistration,
        logout: storeLogout
    } = useAuthStore();

    const isAuthenticated = !!token;

    const getAuthHeaders = useCallback((): Record<string, string> => {
        if (token) {
            return { Authorization: `Bearer ${token}` };
        }
        return {};
    }, [token]);

    const logout = useCallback(() => {
        storeLogout();
        fetch("/api/v1/auth/logout", {
            method: "POST",
            headers: {
                "Authorization": token ? `Bearer ${token}` : "",
            },
        }).catch(() => { });

        navigateToHome();
    }, [token, storeLogout]);


    const login = useCallback((newToken: string) => {
        setToken(newToken);
        setRequiresRegistration(false);
    }, [setToken, setRequiresRegistration]);

    return {
        token,
        isAuthenticated,
        requiresRegistration,
        isInitialized,
        login,
        logout,
        getAuthHeaders
    };
}
