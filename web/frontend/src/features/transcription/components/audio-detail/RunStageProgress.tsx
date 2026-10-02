import {
	Check,
	CircleAlert,
	Clock3,
	FileCheck2,
	Loader2,
	Pause,
	ScanText,
	UsersRound,
	Volume2
} from 'lucide-react';
import { cn } from '@/lib/utils';
import {
	recoveryStageStatusLabel,
	stageLabel,
	type ExecutionRecovery
} from '../../hooks/recoveryPolicy';
import { runProgressSummary, stageVisualState } from '../../hooks/runProgress';

export function RunStageProgress({ recovery }: { recovery: ExecutionRecovery }) {
	const progress = runProgressSummary(recovery);
	if (!progress.total) return null;
	return (
		<div className="min-w-0 space-y-3" aria-label="Stage progress">
			<div className="flex flex-wrap items-center justify-between gap-2 text-xs">
				<p className="font-medium text-[var(--text-primary)]" role="status">
					{progress.label}
				</p>
				<span className="text-[var(--text-tertiary)]">
					{progress.done} of {progress.total} stages
				</span>
			</div>
			<div className="min-w-0 overflow-x-auto pb-1">
				<ol
					className="grid gap-0"
					style={{ gridTemplateColumns: `repeat(${progress.total}, minmax(72px, 1fr))` }}
				>
					{recovery.stages.map((stage, index) => {
						const state = stageVisualState(stage);
						const PhaseIcon = ['recognition', 'recognize', 'asr', 'combined'].includes(stage.kind)
							? Volume2
							: ['alignment', 'align'].includes(stage.kind)
								? ScanText
								: ['diarize', 'diarization', 'speaker_assignment'].includes(stage.kind)
									? UsersRound
									: FileCheck2;
						const Icon =
							state === 'done'
								? Check
								: state === 'active'
									? Loader2
									: state === 'waiting'
										? Clock3
										: state === 'issue'
											? Pause
											: PhaseIcon;
						const failed = ['failed', 'blocked'].includes(stage.status);
						const StatusIcon = failed ? CircleAlert : Icon;
						return (
							<li
								key={stage.id}
								aria-label={`${stageLabel(stage)}: ${recoveryStageStatusLabel(stage)}`}
								aria-current={state === 'active' || state === 'waiting' ? 'step' : undefined}
								className="min-w-0 text-center"
							>
								<div className="relative flex h-8 items-center justify-center">
									{index < progress.total - 1 && (
										<span
											aria-hidden
											className={cn(
												'absolute top-1/2 left-1/2 h-px w-full',
												state === 'done' ? 'bg-emerald-500/40' : 'bg-[var(--border-subtle)]'
											)}
										/>
									)}
									<span
										className={cn(
											'relative z-10 flex h-8 w-8 shrink-0 items-center justify-center rounded-full border bg-[var(--bg-card)]',
											state === 'done'
												? 'border-emerald-500/40 text-emerald-600 dark:text-emerald-400'
												: state === 'active'
													? 'border-[var(--brand-solid)] text-[var(--brand-solid)] ring-4 ring-[var(--brand-light)]'
													: state === 'waiting'
														? 'border-amber-500/40 text-amber-600 dark:text-amber-300'
														: state === 'issue'
															? 'border-red-500/40 text-red-600 dark:text-red-300'
															: 'border-[var(--border-subtle)] text-[var(--text-tertiary)]'
										)}
									>
										<StatusIcon
											aria-hidden
											className={cn(
												'h-4 w-4',
												state === 'active' && 'animate-spin motion-reduce:animate-none'
											)}
										/>
									</span>
								</div>
								<p
									className={cn(
										'mt-2 px-1 text-[10px] leading-4 break-words sm:text-xs',
										state === 'pending'
											? 'text-[var(--text-tertiary)]'
											: 'text-[var(--text-secondary)]'
									)}
								>
									{stageLabel(stage)}
								</p>
							</li>
						);
					})}
				</ol>
			</div>
		</div>
	);
}
