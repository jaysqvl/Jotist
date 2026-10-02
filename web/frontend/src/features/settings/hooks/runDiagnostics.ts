import type { ExecutionRun } from '@/features/transcription/hooks/useAudioDetail';

export interface RunHistoryEntry extends ExecutionRun {
	recording_title: string;
	model_family: string;
	model: string;
}

export interface RunHistory {
	runs: RunHistoryEntry[];
	pagination: { page: number; limit: number; total: number; pages: number };
}

export function runDiagnosticsHref(recordingID: string, executionID: string, section?: string) {
	const params = new URLSearchParams({
		tab: 'statistics',
		view: 'runs',
		recording: recordingID,
		run: executionID
	});
	if (section) params.set('section', section);
	return `/settings?${params}`;
}
