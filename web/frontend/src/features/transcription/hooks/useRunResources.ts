import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/features/auth/hooks/useAuth';
import type { RunResources } from './runResources';

export function useRunResources(
	audioID: string,
	executionID?: string,
	enabled = true,
	poll = false
) {
	const { getAuthHeaders } = useAuth();
	return useQuery({
		queryKey: ['runResources', audioID, executionID],
		enabled: enabled && !!audioID && !!executionID,
		queryFn: async (): Promise<RunResources> => {
			const response = await fetch(
				`/api/v1/transcription/${audioID}/runs/${executionID}/resources`,
				{ headers: getAuthHeaders() }
			);
			if (!response.ok) throw new Error('Could not load run resource measurements.');
			return response.json();
		},
		refetchInterval: enabled && poll ? 3000 : false
	});
}
