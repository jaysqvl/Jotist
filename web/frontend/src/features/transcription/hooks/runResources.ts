import type { RecoveryAttempt, RecoveryStage, ResourceMeasurements } from './recoveryPolicy.ts';

export interface RunResources {
	execution_id: string;
	available: boolean;
	invocations: ResourceMeasurements[] | null;
	stages: RecoveryStage[];
}

function maximum(
	rows: ResourceMeasurements[],
	key: keyof ResourceMeasurements
): number | undefined {
	const values = rows
		.map((row) => row[key])
		.filter(
			(value): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0
		);
	return values.length ? Math.max(...values) : undefined;
}

function minimum(
	rows: ResourceMeasurements[],
	key: keyof ResourceMeasurements
): number | undefined {
	const values = rows
		.map((row) => row[key])
		.filter(
			(value): value is number => typeof value === 'number' && Number.isFinite(value) && value >= 0
		);
	return values.length ? Math.min(...values) : undefined;
}

function weighted(
	rows: ResourceMeasurements[],
	valueKey: keyof ResourceMeasurements,
	secondsKey: keyof ResourceMeasurements
) {
	let sum = 0,
		seconds = 0;
	for (const row of rows) {
		const value = row[valueKey],
			duration = row[secondsKey];
		if (
			typeof value !== 'number' ||
			!Number.isFinite(value) ||
			value < 0 ||
			typeof duration !== 'number' ||
			!Number.isFinite(duration) ||
			duration <= 0
		)
			continue;
		sum += value * duration;
		seconds += duration;
	}
	return { value: seconds > 0 ? sum / seconds : undefined, seconds };
}

export function gpuHeadroom(measurements?: ResourceMeasurements | null) {
	const total = measurements?.gpu_total_bytes;
	const peak = measurements?.device_peak_used_bytes;
	if (
		total == null ||
		total <= 0 ||
		!Number.isFinite(total) ||
		peak == null ||
		peak < 0 ||
		!Number.isFinite(peak)
	)
		return undefined;
	const bytes = Math.max(0, total - peak);
	return { bytes, percent: (bytes / total) * 100 };
}

// Invocations of one execution do not overlap. Stage peaks/averages are kept
// separate because parallel tracks may overlap and cannot be summed as a run.
export function summarizeRunResources(invocations: ResourceMeasurements[]) {
	const headroom = invocations.map(gpuHeadroom).filter((value) => value !== undefined);
	return {
		peakRSS: maximum(invocations, 'process_peak_rss_bytes'),
		averageRSS: weighted(invocations, 'process_average_rss_bytes', 'rss_sampled_seconds'),
		peakOwnedVRAM: maximum(invocations, 'process_peak_vram_bytes'),
		averageOwnedVRAM: weighted(
			invocations,
			'process_average_vram_bytes',
			'process_vram_sampled_seconds'
		),
		peakDeviceVRAM: maximum(invocations, 'device_peak_used_bytes'),
		averageDeviceVRAM: weighted(invocations, 'device_average_used_bytes', 'device_sampled_seconds'),
		averageCPU: weighted(invocations, 'process_cpu_average_percent', 'cpu_sampled_seconds'),
		cpuCapacity: minimum(invocations, 'cpu_capacity_cores'),
		minimumHostAvailable: minimum(invocations, 'host_minimum_available_bytes'),
		minimumGPUHeadroom: headroom.length
			? Math.min(...headroom.map((value) => value.bytes))
			: undefined,
		minimumGPUHeadroomPercent: headroom.length
			? Math.min(...headroom.map((value) => value.percent))
			: undefined,
		elapsedSeconds: invocations.reduce(
			(sum, row) => sum + (Number.isFinite(row.elapsed_seconds) ? row.elapsed_seconds : 0),
			0
		),
		samples: invocations.reduce((sum, row) => sum + row.samples, 0),
		externalContention: invocations.some((row) => row.external_contention),
		ownershipUnknown: invocations.some((row) => row.ownership_unknown)
	};
}

export function attemptResourceValues(attempt: RecoveryAttempt) {
	const m = attempt.measurements;
	return {
		peakRSS:
			m?.process_peak_rss_bytes ?? (attempt.device === 'cpu' ? m?.process_peak_bytes : undefined),
		peakOwnedVRAM:
			m?.process_peak_vram_bytes ?? (attempt.device === 'cuda' ? m?.process_peak_bytes : undefined),
		averageRSS: m?.process_average_rss_bytes,
		averageOwnedVRAM: m?.process_average_vram_bytes,
		peakDeviceVRAM: m?.device_peak_used_bytes,
		averageDeviceVRAM: m?.device_average_used_bytes,
		averageCPU: m?.process_cpu_average_percent
	};
}

export function resourceBytes(value?: number): string {
	if (value == null || !Number.isFinite(value) || value < 0) return 'Not recorded';
	const gib = value / 1024 ** 3;
	return gib >= 1 ? `${gib.toFixed(2)} GiB` : `${(value / 1024 ** 2).toFixed(1)} MiB`;
}

export function resourceDuration(seconds?: number): string {
	if (seconds == null || !Number.isFinite(seconds) || seconds < 0) return 'Not recorded';
	if (seconds < 60) return `${seconds.toFixed(1)}s`;
	const minutes = Math.floor(seconds / 60);
	return minutes < 60
		? `${minutes}m ${Math.floor(seconds % 60)}s`
		: `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

export function attemptElapsed(attempt: RecoveryAttempt): number | undefined {
	if (!attempt.started_at || !attempt.completed_at) return undefined;
	const seconds = (Date.parse(attempt.completed_at) - Date.parse(attempt.started_at)) / 1000;
	return Number.isFinite(seconds) && seconds >= 0 ? seconds : undefined;
}
