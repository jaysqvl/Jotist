import {
	recoveryStageStatusLabel,
	stageLabel,
	type ExecutionRecovery,
	type RecoveryStage
} from './recoveryPolicy.ts';

export type StageVisualState = 'done' | 'active' | 'waiting' | 'issue' | 'pending';

export function stageVisualState(stage: RecoveryStage): StageVisualState {
	if (stage.status === 'succeeded') return 'done';
	if (['running', 'processing'].includes(stage.status)) return 'active';
	if (['waiting_for_resource', 'retryable', 'waiting'].includes(stage.status)) return 'waiting';
	if (['failed', 'blocked', 'interrupted', 'cancelled'].includes(stage.status)) return 'issue';
	return 'pending';
}

export function runProgressSummary(recovery: ExecutionRecovery) {
	const stages = recovery.stages ?? [];
	const done = stages.filter((stage) => stageVisualState(stage) === 'done').length;
	const current =
		stages.find((stage) => stageVisualState(stage) === 'active') ??
		stages.find((stage) => stageVisualState(stage) === 'waiting') ??
		stages.find((stage) => stageVisualState(stage) === 'issue');
	return {
		done,
		total: stages.length,
		current,
		label: current
			? `${stageLabel(current)} · ${recoveryStageStatusLabel(current)}`
			: stages.length > 0 && done === stages.length
				? 'Transcript ready'
				: stages.length === 0
					? 'Stage progress not recorded'
					: ['failed', 'interrupted', 'cancelled', 'blocked'].includes(recovery.status)
						? 'Execution stopped'
						: 'Waiting for the next stage'
	};
}
