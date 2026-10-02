import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/features/auth/hooks/useAuth';
import type { QueueActivity } from './queueActivity';

export const queueActivityKey = ['queueActivity'] as const;

export function useQueueActivity() {
    const { getAuthHeaders } = useAuth();
    return useQuery({
        queryKey: queueActivityKey,
        queryFn: async () => {
            const response = await fetch('/api/v1/admin/queue/activity', { headers: getAuthHeaders() });
            if (!response.ok) throw new Error('Shared queue status is unavailable');
            return await response.json() as QueueActivity;
        },
        staleTime: 2500,
        refetchInterval: 5000,
        retry: 1,
    });
}
