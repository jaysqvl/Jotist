export interface RunMemoryStatistics {
	peak_gpu_bytes: number | null;
	average_gpu_bytes: number | null;
	gpu_sampled_seconds: number;
	peak_worker_gpu_bytes: number | null;
	peak_ram_bytes: number | null;
	average_ram_bytes: number | null;
	ram_sampled_seconds: number;
	contention_runs: number;
	unknown_ownership_runs: number;
}

export interface RunModelStatistics {
	model_family: string;
	model: string;
	diarizer: string;
	recognition_device: string;
	speaker_device: string;
	precision: string;
	runs: number;
	completed: number;
	failed: number;
	recovered: number;
	cpu_fallback: number;
	reused: number;
	timing_measured_runs: number;
	median_hour_seconds: number | null;
	memory: RunMemoryStatistics;
}

export interface RunActivity {
	date: string;
	completed: number;
	failed: number;
	other: number;
}
export interface RunStatistics {
	demo?: boolean;
	generated_at: string;
	window_days: number;
	library_recordings: number;
	summary: {
		runs: number;
		completed: number;
		failed: number;
		active: number;
		other: number;
		recovered: number;
		cpu_fallback: number;
		reused: number;
		resumed: number;
		completion_rate: number | null;
		audio_seconds: number;
		audio_measured_runs: number;
		timing_measured_runs: number;
		median_hour_seconds: number | null;
		resource_measured_runs: number;
		memory: RunMemoryStatistics;
	};
	models: RunModelStatistics[];
	activity: RunActivity[];
	stages: {
		kind: string;
		attempts: number;
		succeeded: number;
		failed: number;
		retries: number;
		worker_retries?: number;
		timing_samples: number;
		median_seconds: number | null;
	}[];
	failures: { stage: string; code: string; attempts: number }[];
}

export function statisticsDuration(seconds: number | null | undefined): string {
	if (seconds == null || !Number.isFinite(seconds) || seconds < 0) return '—';
	const rounded = Math.round(seconds);
	if (rounded < 60) return `${rounded}s`;
	const minutes = Math.floor(rounded / 60);
	return minutes < 60
		? `${minutes}m ${rounded % 60}s`
		: `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

export function statisticsMemory(bytes: number | null | undefined): string {
	if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return '—';
	return `${(bytes / 1024 ** 3).toFixed(1)} GiB`;
}

export function activityDays(rows: RunActivity[], generatedAt: string, days = 30): RunActivity[] {
	const end = new Date(generatedAt.slice(0, 10) + 'T00:00:00Z');
	if (!Number.isFinite(end.getTime())) return [];
	const byDate = new Map(rows.map((row) => [row.date, row]));
	return Array.from({ length: days }, (_, index) => {
		const date = new Date(end);
		date.setUTCDate(date.getUTCDate() - days + index + 1);
		const key = date.toISOString().slice(0, 10);
		return byDate.get(key) ?? { date: key, completed: 0, failed: 0, other: 0 };
	});
}

export function statisticsDevice(device: string) {
	return (
		(
			{
				cuda: 'GPU',
				cpu: 'CPU',
				api: 'Cloud',
				mps: 'Apple GPU',
				none: 'Off',
				unknown: 'Not recorded'
			} as Record<string, string>
		)[device] ?? device
	);
}

export function statisticsStageLabel(kind: string): string {
	return (
		(
			{
				prepare: 'Prepare audio',
				recognition: 'Transcription',
				alignment: 'Word timing',
				diarization: 'Speakers',
				assemble: 'Assemble output',
				publish: 'Publish output',
				combined: 'Combined output',
				asr: 'Transcription',
				recognize: 'Transcription',
				align: 'Word timing',
				diarize: 'Speakers',
				speaker_assignment: 'Speaker assignment'
			} as Record<string, string>
		)[kind] ?? kind.replaceAll('_', ' ')
	);
}
