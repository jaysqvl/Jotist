import type { RunResources } from '../../hooks/runResources';
import {
	attemptElapsed,
	attemptResourceValues,
	resourceBytes,
	resourceDuration,
	summarizeRunResources
} from '../../hooks/runResources';
import { precisionLabel } from '../../hooks/executionPresentation';
import { stageLabel } from '../../hooks/recoveryPolicy';

export function RunResourcesPanel({
	resources,
	loading,
	error,
	running = false
}: {
	resources?: RunResources;
	loading?: boolean;
	error?: string;
	running?: boolean;
}) {
	if (loading)
		return <p className="text-sm text-[var(--text-secondary)]">Loading resource measurements…</p>;
	if (error)
		return (
			<p className="text-sm text-red-600 dark:text-red-300" role="alert">
				{error}
			</p>
		);
	const invocations = resources?.invocations || [];
	const summary = summarizeRunResources(invocations);
	const stages = resources?.stages || [];
	return (
		<section className="space-y-4" aria-label="Run resources and stage timing">
			<div className="rounded-xl border border-[var(--border-subtle)] p-4">
				<h4 className="font-semibold text-[var(--text-primary)]">Run resource usage</h4>
				<p className="mt-1 text-xs text-[var(--text-secondary)]">
					{running
						? 'Measurements are saved when each attempt or invocation finishes.'
						: invocations.length
							? `${invocations.length} measured ${invocations.length === 1 ? 'invocation' : 'invocations'} · ${resourceDuration(summary.elapsedSeconds)} · ${summary.samples} samples`
							: 'This run has no full run measurements. Available stage readings appear below; historical averages cannot be reconstructed.'}
				</p>
				<dl className="mt-4 grid grid-cols-2 gap-4 xl:grid-cols-3">
					<ResourceMetric
						label="Worker RAM (RSS)"
						value={resourceBytes(summary.peakRSS)}
						detail={`Peak · average ${resourceBytes(summary.averageRSS.value)}`}
					/>
					<ResourceMetric
						label="GPU memory (whole device)"
						value={resourceBytes(summary.peakDeviceVRAM)}
						detail={`Peak · average ${resourceBytes(summary.averageDeviceVRAM.value)}`}
					/>
					<ResourceMetric
						label="Average worker CPU"
						value={
							summary.averageCPU.value == null
								? 'Not recorded'
								: `${summary.averageCPU.value.toFixed(1)}%`
						}
						detail={`100% = one logical CPU${summary.cpuCapacity ? ` · capacity ${summary.cpuCapacity} logical CPUs` : ''}`}
					/>
					<ResourceMetric
						label="Owned process GPU memory"
						value={resourceBytes(summary.peakOwnedVRAM)}
						detail={`Peak · average ${resourceBytes(summary.averageOwnedVRAM.value)}`}
					/>
					<ResourceMetric
						label="Minimum available host RAM"
						value={resourceBytes(summary.minimumHostAvailable)}
						detail="Host or container limit"
					/>
					<ResourceMetric
						label="GPU reserve at peak"
						value={resourceBytes(summary.minimumGPUHeadroom)}
						detail={summary.minimumGPUHeadroomPercent == null ? 'Capacity minus whole device peak' : `${summary.minimumGPUHeadroomPercent.toFixed(1)}% of capacity remains at peak`}
					/>
				</dl>
				{invocations.length > 0 && (
					<p className="mt-4 text-[11px] text-[var(--text-secondary)]">
						Averages cover recorded intervals: RAM {resourceDuration(summary.averageRSS.seconds)},
						whole GPU {resourceDuration(summary.averageDeviceVRAM.seconds)}, CPU{' '}
						{resourceDuration(summary.averageCPU.seconds)}. Paused time between resumes is excluded.
					</p>
				)}
				{summary.externalContention && (
					<p className="mt-2 text-xs text-amber-700 dark:text-amber-300">
						Another GPU consumer was observed. Whole device usage includes it.
					</p>
				)}
				{summary.ownershipUnknown && (
					<p className="mt-2 text-xs text-[var(--text-secondary)]">
						GPU process ownership was unavailable for some readings. Whole device readings and owned
						process readings are shown separately.
					</p>
				)}
			</div>
			<div className="space-y-3">
				<h4 className="font-semibold text-[var(--text-primary)]">Stages and attempts</h4>
				{stages.length === 0 && (
					<p className="text-sm text-[var(--text-secondary)]">
						No stage timing was recorded for this run.
					</p>
				)}
				{stages.map((stage) => (
					<section key={stage.id} className="rounded-xl border border-[var(--border-subtle)] p-4">
						<div className="flex flex-wrap items-center justify-between gap-2">
							<h5 className="text-sm font-semibold text-[var(--text-primary)]">
								{stageLabel(stage)}
							</h5>
							<span className="text-xs text-[var(--text-secondary)]">
								{stage.status.replaceAll('_', ' ')}
							</span>
						</div>
						{stage.reused_from_execution_id && (
							<p className="mt-2 text-xs text-[var(--text-secondary)]">
								Reused checkpoint. Prior model work is not a new inference measurement.
							</p>
						)}
						{stage.attempts.map((attempt) => {
							const values = attemptResourceValues(attempt),
								m = attempt.measurements;
							return (
								<div
									key={attempt.id}
									className="mt-3 border-t border-[var(--border-subtle)] pt-3 text-xs"
								>
									<div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[var(--text-primary)]">
										<strong>Attempt {attempt.attempt_number}</strong>
										<span>{attempt.status.replaceAll('_', ' ')}</span>
										<span>
											{attempt.device === 'cuda'
												? 'GPU'
												: attempt.device === 'cpu'
													? 'CPU'
													: attempt.device || 'Device not recorded'}{' '}
											·{' '}
											{attempt.precision
												? precisionLabel(attempt.precision)
												: 'Precision not recorded'}
										</span>
										<span>Batch {attempt.batch_size ?? 'not recorded'}</span>
										<span>
											Window{' '}
											{attempt.window_seconds != null && attempt.window_seconds > 0
												? resourceDuration(attempt.window_seconds)
												: (attempt.plan_version ?? 0) > 0
													? 'full audio'
													: 'not recorded'}
										</span>
									</div>
									<p className="mt-1 text-[var(--text-secondary)]">
										Elapsed {resourceDuration(attemptElapsed(attempt))} · measured work{' '}
										{resourceDuration(m?.elapsed_seconds)}
										{attempt.reason && ` · ${attempt.reason.replaceAll('_', ' ')}`}
									</p>
									<dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 xl:grid-cols-3">
										<ResourceMetric
											label="Worker RAM peak / average"
											value={`${resourceBytes(values.peakRSS)} / ${resourceBytes(values.averageRSS)}`}
										/>
										<ResourceMetric
											label="Whole GPU peak / average"
											value={`${resourceBytes(values.peakDeviceVRAM)} / ${resourceBytes(values.averageDeviceVRAM)}`}
										/>
										<ResourceMetric
											label="Average worker CPU"
											value={
												values.averageCPU == null
													? 'Not recorded'
													: `${values.averageCPU.toFixed(1)}%`
											}
										/>
										<ResourceMetric
											label="Owned GPU peak / average"
											value={`${resourceBytes(values.peakOwnedVRAM)} / ${resourceBytes(values.averageOwnedVRAM)}`}
										/>
										<ResourceMetric
											label="PyTorch allocated / reserved peaks"
											value={`${resourceBytes(m?.torch_peak_allocated_bytes)} / ${resourceBytes(m?.torch_peak_reserved_bytes)}`}
										/>
										<ResourceMetric
											label="Minimum host RAM available"
											value={resourceBytes(m?.host_minimum_available_bytes)}
										/>
									</dl>
									{attempt.error_code && (
										<p className="mt-2 text-red-600 dark:text-red-300">
											Error: {attempt.error_code}
										</p>
									)}
									{m?.external_contention && (
										<p className="mt-2 text-amber-700 dark:text-amber-300">
											Other GPU work was observed.
										</p>
									)}
									{m?.ownership_unknown && (
										<p className="mt-2 text-[var(--text-secondary)]">
											GPU process ownership is not fully known.
										</p>
									)}
								</div>
							);
						})}
					</section>
				))}
			</div>
			<p className="text-[11px] text-[var(--text-secondary)]">
				Readings are sampled every 500 ms and may miss short lived children or brief peaks. Worker
				RAM sums process RSS, including shared pages. Whole GPU values include all processes. A
				successful run or spare memory alone does not establish a safe larger batch size.
			</p>
		</section>
	);
}

function ResourceMetric({
	label,
	value,
	detail
}: {
	label: string;
	value: string;
	detail?: string;
}) {
	return (
		<div>
			<dt className="text-[11px] text-[var(--text-secondary)]">{label}</dt>
			<dd className="mt-0.5 text-sm font-medium break-words text-[var(--text-primary)] tabular-nums">
				{value}
				{detail && (
					<p className="mt-1 text-[10px] font-normal text-[var(--text-secondary)]">{detail}</p>
				)}
			</dd>
		</div>
	);
}
