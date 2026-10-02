import assert from 'node:assert/strict';
import test from 'node:test';
import {
	filterRunChoices,
	runChoicePresentation,
	runElapsedLabel,
	runOutcomeLabel,
	runRecoveryBehavior,
	runStatusLabel,
	type RunChoice
} from './runChoices.ts';

test('worker actions appear once, independently of the selected recovery policy', () => {
    const run: RunChoice = { id: 'worker-recovered', run_number: 1, status: 'completed',
        runtime_metadata: { auto_token_split_windows: '1', token_retries: '1' },
        recovery_summary: { evidence_available: true, retry_count: 0, cpu_fallback: false, reasons: [], worker_retry_count: 2,
            worker_recovery_actions: { token_window_split: 1, decoder_budget_retry: 1 } } };
    assert.equal(runRecoveryBehavior(run).details, '1 window recovery · 1 decoder retry');
    assert.equal(runOutcomeLabel(run), 'Completed with recovery');
    const ordinary = { ...run, runtime_metadata: {}, recovery_summary: { ...run.recovery_summary!, worker_retry_count: 0, worker_recovery_actions: {} } };
    assert.equal(runOutcomeLabel(ordinary), 'Completed');
});

const gpu: RunChoice = {
	id: 'gpu',
	run_number: 2,
	status: 'completed',
	profile_name: 'Meeting GPU',
	processing_duration: 306000,
	actual_parameters: {
		model_family: 'whisper',
		model: 'large-v3',
		device: 'cuda',
		compute_type: 'float16',
		diarize: true,
		diarize_model: 'pyannote',
		diarization_device: 'cpu'
	},
	runtime_metadata: {
		resolved_device: 'cuda',
		precision: 'float16',
		diarization_resolved_device: 'cpu'
	}
};
const fallback: RunChoice = {
	...gpu,
	id: 'fallback',
	run_number: 3,
	profile_name: 'Meeting fallback',
	runtime_metadata: {
		resolved_device: 'cpu',
		precision: 'float32',
		diarization_resolved_device: 'cpu'
	}
};
const failed: RunChoice = {
	id: 'failed',
	run_number: 4,
	status: 'failed',
	actual_parameters: {
		model_family: 'moss_asr',
		model: 'OpenMOSS-Team/MOSS-Transcribe-Diarize',
		device: 'cuda',
		compute_type: 'float16',
		diarize: true,
		diarize_model: 'native'
	}
};

test('run choices distinguish requested settings, actual CPU fallback, and separate speaker devices', () => {
	assert.equal(runChoicePresentation(gpu).device, 'GPU');
	assert.equal(runChoicePresentation(gpu).speakerRuntime, 'CPU');
	assert.equal(runChoicePresentation(fallback).device, 'CPU (requested GPU)');
	assert.equal(runChoicePresentation(fallback).precision, 'FP32');
	assert.equal(runChoicePresentation(failed).device, 'GPU requested');
	assert.equal(runChoicePresentation(failed).speakers, 'Native speakers');
	assert.equal(runChoicePresentation({ id: 'old', run_number: 1 }).device, 'Device not recorded');
});

test('CPU/GPU filters follow recorded stage devices, retain failures, and search profile and model together', () => {
	const runs = [gpu, fallback, failed];
	assert.deepEqual(
		filterRunChoices(runs, '', 'all', 'cuda').map((run) => run.id),
		['gpu', 'failed']
	);
	assert.deepEqual(
		filterRunChoices(runs, '', 'all', 'cpu').map((run) => run.id),
		['gpu', 'fallback']
	);
	assert.deepEqual(
		filterRunChoices(runs, '', 'failed', 'all').map((run) => run.id),
		['failed']
	);
	assert.deepEqual(
		filterRunChoices(runs, 'meeting whisper', 'completed', 'all').map((run) => run.id),
		['gpu']
	);
	assert.deepEqual(
		filterRunChoices(runs, 'meeting whisper', 'recovered', 'all').map((run) => run.id),
		['fallback']
	);
	assert.deepEqual(
		filterRunChoices(runs, 'run 3', 'all', 'all').map((run) => run.id),
		['fallback']
	);
});

test('completed recovery is distinct from completion and failure, while Auto and mixed stages are ordinary completion', () => {
	assert.equal(runOutcomeLabel(gpu), 'Completed'); // GPU recognition with explicitly requested CPU speakers.
	assert.equal(runOutcomeLabel(fallback), 'Completed with recovery');
	assert.equal(runRecoveryBehavior(fallback).details, 'CPU fallback');
	const retried = {
		...gpu,
		recovery_summary: {
			evidence_available: true,
			retry_count: 2,
			cpu_fallback: false,
			reasons: ['smaller_batch']
		}
	};
	assert.equal(runOutcomeLabel(retried), 'Completed with recovery');
	assert.equal(runRecoveryBehavior(retried).details, '2 stage retries');
	assert.equal(runOutcomeLabel({ ...retried, status: 'failed' }), 'Failed');
	assert.equal(
		runOutcomeLabel({
			...fallback,
			actual_parameters: { ...fallback.actual_parameters, device: 'auto' }
		}),
		'Completed'
	);
	const speakerFallback: RunChoice = {
		...gpu,
		actual_parameters: { ...gpu.actual_parameters, device: 'cpu', diarization_device: 'cuda' },
		runtime_metadata: { resolved_device: 'cpu', diarization_resolved_device: 'cpu' }
	};
	assert.equal(runOutcomeLabel(speakerFallback), 'Completed with recovery');
	assert.equal(runRecoveryBehavior(speakerFallback).details, 'CPU fallback');
	assert.equal(
		runOutcomeLabel({
			...speakerFallback,
			actual_parameters: { ...speakerFallback.actual_parameters, diarization_device: 'auto' }
		}),
		'Completed'
	);
	assert.equal(runOutcomeLabel({ id: 'legacy', run_number: 1, status: 'completed' }), 'Completed');
});

test('unknown terminal outcomes remain accessible and running times do not grow after completion', () => {
	for (const status of ['cancelled', 'interrupted', 'unknown']) {
		assert.equal(
			filterRunChoices([{ id: status, run_number: 1, status }], '', 'all', 'all').length,
			1
		);
	}
	assert.equal(runStatusLabel('processing'), 'Running');
	assert.equal(runStatusLabel('failed'), 'Failed');
	assert.equal(runElapsedLabel(gpu), '5m 6s');
	assert.equal(runElapsedLabel({ id: 'old', run_number: 1 }), 'Time not recorded');
	assert.equal(
		runElapsedLabel({ id: 'bad', run_number: 1, processing_duration: -1 }),
		'Time not recorded'
	);
	const timed = {
		id: 'timed',
		run_number: 1,
		status: 'completed',
		started_at: '2026-09-27T10:00:00Z',
		completed_at: '2026-09-27T11:20:00Z'
	};
	assert.equal(runElapsedLabel(timed, Date.parse('2026-09-28T10:00:00Z')), '1h 20m');
});

test('window recovery is visible and failed retried runs remain failed', () => {
	const recovered: RunChoice = {
		...gpu,
		runtime_metadata: { ...gpu.runtime_metadata, auto_token_split_windows: '1', native_timing_retry_windows: '2' }
	};
	assert.equal(runOutcomeLabel(recovered), 'Completed with recovery');
	assert.equal(runRecoveryBehavior(recovered).details, '3 window recoveries');
	assert.equal(runOutcomeLabel({ ...recovered, status: 'failed' }), 'Failed');
	assert.equal(runOutcomeLabel({ ...gpu, runtime_metadata: { token_retries: 'private' } }), 'Completed');
	const repaired = { ...gpu, runtime_metadata: { output_repair_count: '1' } };
	assert.equal(runOutcomeLabel(repaired), 'Completed with recovery');
	assert.equal(runRecoveryBehavior(repaired).details, '1 output repair');
});

test('cloud recognition keeps local speaker devices separate and does not imply an ASR fallback', () => {
	const cloud: RunChoice = {
		id: 'cloud',
		run_number: 1,
		status: 'completed',
		actual_parameters: {
			model_family: 'openai',
			device: 'cuda',
			diarize: true,
			diarize_model: 'pyannote',
			diarization_device: 'same'
		}
	};
	assert.equal(runChoicePresentation(cloud).device, 'Cloud requested');
	assert.equal(runChoicePresentation(cloud).speakerRuntime, 'GPU requested');
	assert.deepEqual(
		filterRunChoices([cloud], '', 'all', 'cuda').map((run) => run.id),
		['cloud']
	);
	assert.equal(
		runOutcomeLabel({
			...cloud,
			actual_parameters: { ...cloud.actual_parameters, diarize: false },
			runtime_metadata: { resolved_device: 'cpu' }
		}),
		'Completed'
	);
});
