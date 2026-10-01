import assert from 'node:assert/strict';
import test from 'node:test';
import { runProgressSummary, stageVisualState } from './runProgress.ts';
import type { ExecutionRecovery } from './recoveryPolicy.ts';

function recovery(states: string[], status = 'running'): ExecutionRecovery {
	return {
		execution_id: 'run',
		status,
		resumable: false,
		partial_transcript_available: false,
		stages: states.map((state, index) => ({
			id: `stage-${index}`,
			kind: ['recognition', 'alignment', 'diarization'][index],
			status: state,
			attempts: []
		}))
	};
}

test('progress shows the saved current stage without estimating an audio percentage', () => {
	const summary = runProgressSummary(recovery(['succeeded', 'running', 'pending']));
	assert.equal(summary.done, 1);
	assert.equal(summary.total, 3);
	assert.equal(summary.current?.kind, 'alignment');
	assert.equal(stageVisualState(summary.current!), 'active');
	assert.match(summary.label, /Running/);
	assert.equal(runProgressSummary(recovery(['pending', 'pending'])).current, undefined);
});

test('waiting, failures and missing progress are not drawn as successful work', () => {
	assert.equal(stageVisualState(recovery(['waiting_for_resource']).stages[0]), 'waiting');
	const failure = runProgressSummary(recovery(['succeeded', 'failed', 'pending'], 'failed'));
	assert.equal(failure.done, 1);
	assert.equal(failure.current?.kind, 'alignment');
	assert.equal(stageVisualState(failure.current!), 'issue');
	assert.match(failure.label, /Failed/);
	assert.equal(runProgressSummary(recovery([], 'completed')).label, 'Stage progress not recorded');
	assert.equal(
		runProgressSummary(recovery(['succeeded', 'succeeded'], 'succeeded')).label,
		'Transcript ready'
	);
});
