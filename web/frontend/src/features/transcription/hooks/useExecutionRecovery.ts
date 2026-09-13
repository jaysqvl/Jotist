import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";
import { recoveryIsActive, type ExecutionRecovery } from "./recoveryPolicy";

async function apiError(response: Response, fallback: string) {
    try {
        const data = await response.json();
        return typeof data.error === "string" ? data.error : fallback;
    } catch { return fallback; }
}

export function useExecutionRecovery(audioID: string, executionID?: string, poll = false) {
    const { getAuthHeaders } = useAuth();
    return useQuery({
        queryKey: ["executionRecovery", audioID, executionID],
        enabled: !!audioID && !!executionID,
        queryFn: async (): Promise<ExecutionRecovery | null> => {
            const response = await fetch(`/api/v1/transcription/${audioID}/runs/${executionID}/recovery`, { headers: getAuthHeaders() });
            if (response.status === 404) return null;
            if (!response.ok) throw new Error(await apiError(response, "Could not load recovery details."));
            return response.json();
        },
        refetchInterval: (query) => poll || recoveryIsActive(query.state.data?.status) ? 3000 : false,
    });
}

export function useResumeExecution(audioID: string) {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: async (executionID: string) => {
            const response = await fetch(`/api/v1/transcription/${audioID}/runs/${executionID}/resume`, {
                method: "POST", headers: getAuthHeaders(),
            });
            if (!response.ok) throw new Error(await apiError(response, "Could not resume this execution."));
        },
        onSuccess: async () => {
            await Promise.all(["executionRecovery", "executionRuns", "executionData", "transcriptionQueue", "audio", "runTranscript", "transcript"].map((key) => queryClient.invalidateQueries({ queryKey: [key, audioID] })));
        },
    });
}
