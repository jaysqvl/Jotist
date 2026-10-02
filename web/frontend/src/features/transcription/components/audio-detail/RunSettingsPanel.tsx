import type { ExecutionRun, Transcript, MultiTrackTiming } from '../../hooks/useAudioDetail';
import { executionEvidenceRows } from '../../hooks/executionPresentation';
import { RunModelSummary } from './RunModelSummary';

export function RunSettingsPanel({
	run,
	transcript
}: {
	run: ExecutionRun;
	transcript?: Transcript | null;
}) {
	const params = run.actual_parameters || {};
	const evidence = transcript || { text: '', metadata: run.runtime_metadata };
	return (
		<section className="min-w-0 space-y-4" aria-label="Saved run settings">
			<div className="rounded-xl border border-[var(--border-subtle)] p-4">
				<h3 className="mb-3 text-sm font-semibold">Models and devices</h3>
				<RunModelSummary parameters={params} transcript={evidence} detailed />
			</div>
			<details className="rounded-xl border border-[var(--border-subtle)]">
				<summary className="cursor-pointer p-4 text-sm font-semibold">
					Saved execution policy
				</summary>
				<dl className="grid grid-cols-1 gap-3 p-4 pt-0 text-xs sm:grid-cols-2">
					{executionEvidenceRows(params, evidence.metadata).map((row) => (
						<div key={row.label}>
							<dt className="text-[var(--text-secondary)]">{row.label}</dt>
							<dd className="mt-1 text-[var(--text-primary)]">{row.value}</dd>
						</div>
					))}
				</dl>
			</details>
			<details className="rounded-xl border border-[var(--border-subtle)]">
				<summary className="cursor-pointer p-4 text-sm font-semibold">
					All configuration parameters
				</summary>
				<div className="min-w-0 p-4 pt-0">
					<CuratedParamsDisplay params={params} />
				</div>
			</details>
		</section>
	);
}

export function RunTrackTimings({ timings }: { timings: MultiTrackTiming[] }) {
	return (
		<div className="space-y-3">
			{timings.map((timing, index) => (
				<div
					key={`${timing.track_name}-${index}`}
					className="flex flex-col gap-2 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-main)] p-3"
				>
					<div className="flex items-start justify-between gap-2">
						<span className="text-sm leading-tight font-medium break-all text-[var(--text-primary)]">
							{timing.track_name}
						</span>
						<span className="flex-shrink-0 font-mono text-sm font-bold text-[var(--brand-solid)]">
							{formatDuration(timing.duration)}
						</span>
					</div>
					<div className="flex justify-between rounded-[var(--radius-sm)] bg-[var(--bg-card)]/60 p-1.5 text-[11px] text-[var(--text-tertiary)]">
						<span>
							{new Date(timing.start_time).toLocaleTimeString([], {
								hour: '2-digit',
								minute: '2-digit',
								second: '2-digit',
								hour12: false
							})}
						</span>
						<span>to</span>
						<span>
							{new Date(timing.end_time).toLocaleTimeString([], {
								hour: '2-digit',
								minute: '2-digit',
								second: '2-digit',
								hour12: false
							})}
						</span>
					</div>
				</div>
			))}
		</div>
	);
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function CuratedParamsDisplay({ params }: { params: any }) {
	const commonKeys = [
		'model_family',
		'model',
		'task',
		'language',
		'output_format',
		'device',
		'compute_type',
		'batch_size',
		'threads',
		'diarize',
		'diarize_model',
		'diarization_device',
		'diarization_checkpoint',
		'transcription_context',
		'transcription_context_terms',
		'audio_chunk_duration',
		'max_new_tokens',
		'hf_token_source'
	];

	let specificKeys: string[] = [];

	if (params.model_family === 'whisper') {
		specificKeys = [
			'model',
			'compute_type',
			'no_align',
			'vad_method',
			...(params.diarize ? ['diarize_model', 'min_speakers', 'max_speakers', 'hf_token'] : [])
		];
	} else if (params.model_family === 'nvidia_parakeet') {
		specificKeys = [
			'attention_context_left',
			'attention_context_right',
			'nvidia_chunk_duration',
			'nvidia_timestamps',
			...(params.diarize ? ['diarize_model'] : [])
		];
	} else if (params.model_family === 'openai') {
		specificKeys = ['model', 'api_key'];
	} else if (params.model_family === 'nvidia_canary') {
		specificKeys = [
			'nvidia_target_language',
			'nvidia_timestamps',
			'nvidia_use_chunking',
			'nvidia_chunk_duration',
			'nvidia_precision',
			...(params.diarize ? ['diarize_model'] : [])
		];
	} else if (params.model_family === 'nvidia_canary_qwen') {
		specificKeys = [
			'nvidia_chunk_duration',
			'nvidia_timestamps',
			'nvidia_precision',
			'max_new_tokens',
			'nvidia_prompt',
			...(params.diarize ? ['diarize_model'] : [])
		];
	} else {
		specificKeys = [
			'model',
			'compute_type',
			'audio_chunk_duration',
			'max_new_tokens',
			'no_align',
			...(params.diarize ? ['diarize_model'] : [])
		];
	}

	const entries = [...new Set([...commonKeys, ...specificKeys])]
		.map((key) => {
			let value = params[key];
			if (value === undefined || value === null) return null;
			if (typeof value === 'boolean') value = value ? 'Yes' : 'No';
			if (key === 'hf_token' || key === 'api_key') value = '******';
			return { key: formatParamKey(key), value: String(value) };
		})
		.filter((entry): entry is { key: string; value: string } => entry !== null);

	return (
		<div className="grid grid-cols-1 gap-x-4 gap-y-2 text-sm sm:grid-cols-2">
			{entries.map((entry) => (
				<div
					key={entry.key}
					className="flex items-center justify-between gap-4 border-b border-[var(--border-subtle)] py-1 last:border-0 sm:last:border-b"
				>
					<span className="text-[var(--text-secondary)]">{entry.key}</span>
					<span className="text-right font-mono text-xs font-medium break-all text-[var(--text-primary)]">
						{entry.value}
					</span>
				</div>
			))}
		</div>
	);
}

function formatParamKey(key: string): string {
	return key
		.split('_')
		.map((word) => word.charAt(0).toUpperCase() + word.slice(1))
		.join(' ');
}

function formatDuration(value?: number | null) {
	if (!value || value <= 0) return '...';
	const seconds = Math.round(value / 1000);
	if (seconds < 60) return `${seconds}s`;
	const minutes = Math.floor(seconds / 60);
	const remainingSeconds = seconds % 60;
	if (minutes < 60) return `${minutes}m ${remainingSeconds}s`;
	const hours = Math.floor(minutes / 60);
	const remainingMinutes = minutes % 60;
	return `${hours}h ${remainingMinutes}m`;
}
