import { useId } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/features/auth/hooks/useAuth";

export interface SpeakerMapping {
    id?: number;
    original_speaker: string;
    custom_name: string;
}

const speakerMappingsKey = (audioId: string) => ["speakerMappings", audioId] as const;

function speakerNames(mappings: SpeakerMapping[]) {
    return Object.fromEntries(mappings.map(({ original_speaker, custom_name }) => [original_speaker, custom_name]));
}

function useFetchSpeakerMappings(audioId: string) {
    const { getAuthHeaders } = useAuth();

    return async ({ signal }: { signal: AbortSignal }) => {
        const response = await fetch(`/api/v1/transcription/${audioId}/speakers`, {
            headers: getAuthHeaders(),
            cache: "no-store",
            signal,
        });
        if (!response.ok) throw new Error("Failed to fetch speaker mappings");
        const mappings: SpeakerMapping[] = await response.json();
        return speakerNames(mappings);
    };
}

export function useSpeakerMappings(audioId: string, enabled: boolean) {
    const queryFn = useFetchSpeakerMappings(audioId);
    return useQuery({
        queryKey: speakerMappingsKey(audioId),
        queryFn,
        enabled: enabled && !!audioId,
    });
}

export function useSpeakerRenameMappings(audioId: string) {
    const sessionId = useId();
    const queryFn = useFetchSpeakerMappings(audioId);
    return useQuery({
        // Each open editor starts with a server read independent of the recording's display cache.
        queryKey: [...speakerMappingsKey(audioId), "rename", sessionId],
        queryFn,
        gcTime: 0,
        staleTime: "static",
    });
}

export function useSaveSpeakerMappings(audioId: string) {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();

    return useMutation({
        mutationKey: [...speakerMappingsKey(audioId), "save"],
        mutationFn: async (names: Record<string, string>) => {
            const mappings = Object.entries(names).map(([original_speaker, custom_name]) => ({
                original_speaker,
                custom_name,
            }));
            const response = await fetch(`/api/v1/transcription/${audioId}/speakers`, {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                    ...getAuthHeaders(),
                },
                body: JSON.stringify({ mappings }),
            });
            if (!response.ok) throw new Error("Failed to save speaker names");
            const saved: SpeakerMapping[] = await response.json();
            return speakerNames(saved);
        },
        onSuccess: async (names) => {
            const queryKey = speakerMappingsKey(audioId);
            await queryClient.cancelQueries({ queryKey, exact: true });
            queryClient.setQueryData(queryKey, names);
        },
    });
}
