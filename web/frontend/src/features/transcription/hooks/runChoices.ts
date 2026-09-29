import type { WhisperXParams } from '../types.ts';
import {
	diarizationModelLabel,
	precisionLabel,
	requestedExecutionPrecision
} from './executionPresentation.ts';
import { requestedDiarizationDevice, transcriptionModelLabel } from './modelCapabilities.ts';

export interface RunChoice {
	id: string;
	run_number: number;
	status?: string;
	profile_name?: string;
	actual_parameters?: Partial<WhisperXParams>;
	runtime_metadata?: Record<string, string>;
	recovery_summary?: {
		evidence_available: boolean;
		retry_count: number;
		cpu_fallback: boolean;
		reasons: string[];
	};
	processing_duration?: number | null;
	started_at?: string;
	completed_at?: string | null;
}

export type RunStatusFilter = 'all' | 'completed' | 'recovered' | 'failed' | 'in_progress';
export type RunDeviceFilter = 'all' | 'cuda' | 'cpu';

export function runStatusLabel(status?: string): string {
	const normalized = status?.toLowerCase() || 'unknown';
	return (
		(
			{
				completed: 'Completed',
				failed: 'Failed',
				processing: 'Running',
				running: 'Running',
				pending: 'Pending',
				queued: 'Queued',
				cancelled: 'Cancelled',
				interrupted: 'Interrupted',
				waiting_for_resource: 'Waiting for resources',
				unknown: 'Not recorded'
			} as Record<string, string>
		)[normalized] || normalized.replaceAll('_', ' ')
	);
}

export function runRecoveryBehavior(run: RunChoice) {
	const retries = run.recovery_summary?.retry_count || 0;
	const count = (key: string) => {
		const raw = run.runtime_metadata?.[key] || '';
		return /^\d+$/.test(raw) && Number(raw) <= 100000 ? Number(raw) : 0;
	};
	const windows = count('auto_token_split_windows') + count('native_timing_retry_windows');
	const decoderRetries = count('token_retries');
	const outputRepairs = count('output_repair_count');
	const params = run.actual_parameters || {};
	const localASR = !['openai', 'openai_whisper'].includes(params.model_family || '');
	const requestedSpeaker = requestedDiarizationDevice(
		params.model_family,
		params.diarization_device
	);
	const speakerDevice = requestedSpeaker === 'same' ? params.device : requestedSpeaker;
	const cpuFallback =
		run.recovery_summary?.cpu_fallback === true ||
		run.runtime_metadata?.asr_device_fallback === 'cuda_to_cpu' ||
		run.runtime_metadata?.diarization_device_fallback === 'cuda_to_cpu' ||
		(localASR &&
			normalizeDevice(params.device) === 'cuda' &&
			normalizeDevice(run.runtime_metadata?.resolved_device) === 'cpu') ||
		(params.diarize === true &&
			params.diarize_model !== 'native' &&
			normalizeDevice(speakerDevice) === 'cuda' &&
			normalizeDevice(
				run.runtime_metadata?.diarization_resolved_device ||
					run.runtime_metadata?.diarization_device
			) === 'cpu');
	const details = [
		cpuFallback ? 'CPU fallback' : '',
		retries ? `${retries} stage ${retries === 1 ? 'retry' : 'retries'}` : '',
		windows ? `${windows} window ${windows === 1 ? 'recovery' : 'recoveries'}` : '',
		decoderRetries ? `${decoderRetries} decoder ${decoderRetries === 1 ? 'retry' : 'retries'}` : '',
		outputRepairs ? `${outputRepairs} output ${outputRepairs === 1 ? 'repair' : 'repairs'}` : ''
	].filter(Boolean);
	return { recovered: retries > 0 || windows > 0 || decoderRetries > 0 || outputRepairs > 0 || cpuFallback, details: details.join(' · ') };
}

export function runOutcome(run: RunChoice): string {
	const status = run.status?.toLowerCase();
	return status === 'completed' && runRecoveryBehavior(run).recovered
		? 'recovered'
		: status || 'unknown';
}

export function runOutcomeLabel(run: RunChoice): string {
	return runOutcome(run) === 'recovered' ? 'Completed with recovery' : runStatusLabel(run.status);
}

function normalizeDevice(value?: string): string | undefined {
	if (!value) return undefined;
	const device = value.toLowerCase();
	return device === 'gpu' || device.startsWith('cuda') ? 'cuda' : device;
}

function deviceLabel(value?: string): string {
	const normalized = normalizeDevice(value);
	return (
		(
			{ cuda: 'GPU', cpu: 'CPU', auto: 'Auto', mps: 'Apple GPU', api: 'Cloud' } as Record<
				string,
				string
			>
		)[normalized || ''] ||
		value ||
		'Device not recorded'
	);
}

function runtimeDevice(recorded?: string, requested?: string): string {
	if (!recorded) return requested ? `${deviceLabel(requested)} requested` : 'Device not recorded';
	const changed =
		requested &&
		!['same', 'auto'].includes(requested) &&
		normalizeDevice(recorded) !== normalizeDevice(requested);
	return `${deviceLabel(recorded)}${changed ? ` (requested ${deviceLabel(requested)})` : ''}`;
}

export function runChoicePresentation(run: RunChoice) {
	const params = run.actual_parameters || {};
	const meta = run.runtime_metadata || {};
	const cloud = ['openai', 'openai_whisper'].includes(params.model_family || '');
	const requestedASR = cloud ? 'api' : params.device;
	const asrDevice = normalizeDevice(meta.resolved_device || requestedASR);
	const requestedSpeaker = requestedDiarizationDevice(
		params.model_family,
		params.diarization_device
	);
	const requestedSpeakerDevice = requestedSpeaker === 'same' ? params.device : requestedSpeaker;
	const native = params.diarize_model === 'native';
	const speakerDevice = params.diarize
		? normalizeDevice(
				native
					? meta.resolved_device || requestedASR
					: meta.diarization_resolved_device || meta.diarization_device || requestedSpeakerDevice
			)
		: undefined;
	const model = transcriptionModelLabel(params.model_family, params.model);
	const speakers = !params.diarize
		? 'No speakers'
		: native
			? 'Native speakers'
			: diarizationModelLabel(
					meta.diarization_model ||
						meta.diarization_model_id ||
						params.diarization_checkpoint ||
						params.diarize_model
				);
	const device = runtimeDevice(meta.resolved_device, requestedASR);
	const precision = meta.precision
		? precisionLabel(meta.precision)
		: params.model_family || params.compute_type || params.nvidia_precision
			? `${requestedExecutionPrecision(params)} requested`
			: 'Precision not recorded';
	const speakerRuntime =
		!params.diarize || native
			? ''
			: runtimeDevice(
					meta.diarization_resolved_device || meta.diarization_device,
					requestedSpeakerDevice
				);
	const devices = [asrDevice, speakerDevice].filter((value): value is string => !!value);
	return {
		model,
		speakers,
		device,
		precision,
		speakerRuntime,
		devices,
		status: runOutcomeLabel(run)
	};
}

export function runElapsedLabel(run: RunChoice, now = Date.now()): string {
	let milliseconds = run.processing_duration;
	if (milliseconds == null && run.started_at) {
		const start = Date.parse(run.started_at);
		const end = run.completed_at
			? Date.parse(run.completed_at)
			: ['processing', 'running', 'waiting_for_resource'].includes(run.status || '')
				? now
				: NaN;
		if (Number.isFinite(start) && Number.isFinite(end)) milliseconds = end - start;
	}
	if (milliseconds == null || !Number.isFinite(milliseconds) || milliseconds < 0)
		return 'Time not recorded';
	const seconds = Math.floor(milliseconds / 1000);
	if (seconds < 60) return `${seconds}s`;
	const minutes = Math.floor(seconds / 60);
	return minutes < 60
		? `${minutes}m ${seconds % 60}s`
		: `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

export function filterRunChoices<T extends RunChoice>(
	runs: T[],
	query: string,
	status: RunStatusFilter,
	device: RunDeviceFilter
): T[] {
	const runNumber = query.match(/\brun\s+#?(\d+)\b/i);
	const terms = query
		.replace(/\brun\s+#?\d+\b/i, '')
		.toLowerCase()
		.trim()
		.split(/\s+/)
		.filter(Boolean);
	return runs.filter((run) => {
		if (runNumber && run.run_number !== Number(runNumber[1])) return false;
		const normalizedStatus = run.status?.toLowerCase();
		if (
			status === 'in_progress' &&
			!['processing', 'running', 'pending', 'queued', 'waiting_for_resource'].includes(
				normalizedStatus || ''
			)
		)
			return false;
		if (['completed', 'recovered', 'failed'].includes(status) && runOutcome(run) !== status)
			return false;
		const presentation = runChoicePresentation(run);
		if (device !== 'all' && !presentation.devices.includes(device)) return false;
		const searchable = [
			`Run ${run.run_number}`,
			run.profile_name,
			presentation.model,
			presentation.speakers,
			presentation.device,
			presentation.speakerRuntime,
			presentation.precision,
			presentation.status,
			runRecoveryBehavior(run).details,
			run.actual_parameters?.model,
			run.actual_parameters?.diarization_checkpoint
		]
			.join(' ')
			.toLowerCase();
		return terms.every((term) => searchable.includes(term));
	});
}
