import { useMemo, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { Activity, CheckCircle2, Clock3, Cpu, RefreshCw, Timer, Waves } from 'lucide-react';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { Button } from '@/components/ui/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { RunDiagnosticsSettings } from './RunDiagnosticsSettings';
import { transcriptionModelLabel } from '@/features/transcription/hooks/modelCapabilities';
import {
	diarizationModelLabel,
	precisionLabel
} from '@/features/transcription/hooks/executionPresentation';
import {
	activityDays,
	statisticsDevice,
	statisticsStageLabel,
	statisticsDuration,
	statisticsMemory,
	type RunStatistics,
	type RunModelStatistics
} from '../hooks/runStatistics';

const panel =
	'min-w-0 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-card)] p-4 sm:p-5';

export function RunStatisticsSettings() {
	const [searchParams, setSearchParams] = useSearchParams();
	const view = searchParams.get('view') === 'runs' ? 'runs' : 'overview';
	const [days, setDays] = useState(30);
	const [query, setQuery] = useState('');
	const { getAuthHeaders } = useAuth();
	const { data, isLoading, isFetching, error, refetch } = useQuery({
		queryKey: ['run-statistics', days],
		queryFn: async ({ signal }) => {
			const response = await fetch(`/api/v1/transcription/statistics?days=${days}`, {
				headers: getAuthHeaders(),
				signal
			});
			if (!response.ok) throw new Error('Could not load run statistics.');
			return response.json() as Promise<RunStatistics>;
		},
		staleTime: 30_000
	});
	const groups = useMemo(
		() =>
			(data?.models ?? []).filter((row) =>
				`${transcriptionModelLabel(row.model_family, row.model)} ${diarizationModelLabel(row.diarizer)} ${statisticsDevice(row.recognition_device)} ${statisticsDevice(row.speaker_device)}`
					.toLowerCase()
					.includes(query.toLowerCase().trim())
			),
		[data, query]
	);
	const summary = data?.summary;
	return (
		<section className="min-w-0 space-y-5" aria-labelledby="run-statistics-heading">
			<div className="flex flex-wrap items-start justify-between gap-3">
				<div>
					<h2 id="run-statistics-heading" className="text-xl font-semibold">
						Statistics
						{data?.demo && (
							<span className="ml-2 inline-flex rounded-full bg-[var(--brand-light)] px-2 py-1 align-middle text-[10px] font-medium text-[var(--brand-solid)]">
								Demo data
							</span>
						)}
					</h2>
					<p className="mt-1 text-sm text-[var(--text-secondary)]">
						Processing, recovery and resource usage across your recordings.
					</p>
				</div>
				<div className="flex items-center gap-2">
					<label className="sr-only" htmlFor="statistics-period">
						Statistics period
					</label>
					<select
						id="statistics-period"
						value={days}
						onChange={(event) => setDays(Number(event.target.value))}
						className="h-10 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-main)] px-3 text-sm"
					>
						<option value={7}>Last 7 days</option>
						<option value={30}>Last 30 days</option>
						<option value={90}>Last 90 days</option>
						<option value={0}>All time</option>
					</select>
					<Button
						variant="outline"
						size="icon"
						aria-label="Refresh statistics"
						disabled={isFetching}
						onClick={() => void refetch()}
					>
						<RefreshCw className={`h-4 w-4 ${isFetching ? 'animate-spin' : ''}`} />
					</Button>
				</div>
			</div>
			<Tabs
				value={view}
				onValueChange={(value) => {
					const next = new URLSearchParams(searchParams);
					next.set('view', value);
					next.delete('recording');
					next.delete('run');
					next.delete('section');
					setSearchParams(next);
				}}
				className="min-w-0 space-y-5"
			>
				<TabsList className="h-auto w-full justify-start bg-[var(--bg-main)] sm:w-fit">
					<TabsTrigger value="overview" className="h-11 px-4">
						Overview
					</TabsTrigger>
					<TabsTrigger value="runs" className="h-11 px-4">
						Run diagnostics
					</TabsTrigger>
				</TabsList>
				<TabsContent value="overview" className="min-w-0 space-y-5">
					{isLoading && (
						<p role="status" className="py-12 text-center text-sm text-[var(--text-secondary)]">
							Loading run statistics…
						</p>
					)}
					{error && (
						<div role="alert" className={`${panel} text-sm text-red-600`}>
							Could not load run statistics.{' '}
							<Button variant="outline" size="sm" onClick={() => void refetch()}>
								Try again
							</Button>
						</div>
					)}
					{summary && data && (
						<>
							<div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
								<Metric
									icon={<Activity />}
									label="Total runs"
									value={String(summary.runs)}
									detail={`${data.library_recordings} recordings in your library`}
								/>
								<Metric
									icon={<CheckCircle2 />}
									label="Completion rate"
									value={
										summary.completion_rate == null ? '—' : `${summary.completion_rate.toFixed(1)}%`
									}
									detail={`${summary.completed} completed · ${summary.failed} failed`}
								/>
								<Metric
									icon={<Waves />}
									label="Audio processed"
									value={
										summary.audio_measured_runs ? statisticsDuration(summary.audio_seconds) : '—'
									}
									detail={`${summary.audio_measured_runs} completed runs with duration`}
								/>
								<Metric
									icon={<Timer />}
									label="Typical time for 1h"
									value={statisticsDuration(summary.median_hour_seconds)}
									detail={`Estimate · ${summary.timing_measured_runs} fresh timed runs`}
								/>
							</div>
							{summary.runs === 0 && (
								<div className={`${panel} py-10 text-center`}>
									<h3 className="font-medium">No runs in this period</h3>
									<p className="mt-1 text-sm text-[var(--text-secondary)]">
										Try a longer period, or run a transcription to start collecting statistics.
									</p>
								</div>
							)}
							<div className="grid min-w-0 gap-4 lg:grid-cols-[1.4fr_1fr]">
								<ActivityChart data={data} days={days} />
								<section className={panel} aria-labelledby="statistics-recovery">
									<h3 id="statistics-recovery" className="text-sm font-semibold">
										Recovery & execution
									</h3>
									<dl className="mt-4 space-y-3">
										<StatRow label="Completed with recovery" value={summary.recovered} />
										<StatRow label="CPU fallback observed" value={summary.cpu_fallback} />
										<StatRow label="Runs with reused stages" value={summary.reused} />
										<StatRow label="Resumed executions" value={summary.resumed} />
										<StatRow label="Currently active" value={summary.active} />
										<StatRow label="Cancelled, interrupted or other" value={summary.other} />
									</dl>
								</section>
							</div>
							<section className={panel} aria-labelledby="statistics-memory">
								<div className="flex flex-wrap items-baseline justify-between gap-2">
									<h3
										id="statistics-memory"
										className="flex items-center gap-2 text-sm font-semibold"
									>
										<Cpu className="h-4 w-4 text-[var(--brand-solid)]" />
										Memory usage
									</h3>
									<span className="text-xs text-[var(--text-secondary)]">
										{summary.resource_measured_runs} runs with full-run measurements
									</span>
								</div>
								<div className="mt-4 grid grid-cols-2 gap-x-4 gap-y-5 xl:grid-cols-4">
									<MemoryMetric
										label="GPU peak"
										value={statisticsMemory(summary.memory.peak_gpu_bytes)}
										note="Whole device"
									/>
									<MemoryMetric
										label="GPU average"
										value={statisticsMemory(summary.memory.average_gpu_bytes)}
										note="Weighted by sampled time"
									/>
									<MemoryMetric
										label="Worker RAM peak"
										value={statisticsMemory(summary.memory.peak_ram_bytes)}
										note="Process RSS"
									/>
									<MemoryMetric
										label="Worker RAM average"
										value={statisticsMemory(summary.memory.average_ram_bytes)}
										note="Weighted by sampled time"
									/>
								</div>
								{(summary.memory.contention_runs > 0 ||
									summary.memory.unknown_ownership_runs > 0) && (
									<p className="mt-4 text-xs text-[var(--text-secondary)]">
										Other GPU work observed in {summary.memory.contention_runs} runs · ownership
										unavailable in {summary.memory.unknown_ownership_runs} runs.
									</p>
								)}
							</section>
							<section className={panel} aria-labelledby="statistics-models">
								<div className="flex flex-wrap items-center justify-between gap-3">
									<div>
										<h3 id="statistics-models" className="text-sm font-semibold">
											Models & speaker combinations
										</h3>
										<p className="mt-1 text-xs text-[var(--text-secondary)]">
											Observed devices · one-hour times are estimates from fresh completed runs.
										</p>
									</div>
									<input
										type="search"
										aria-label="Search statistics models"
										placeholder="Find a model or device…"
										value={query}
										onChange={(e) => setQuery(e.target.value)}
										className="h-10 w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-main)] px-3 text-sm sm:w-60"
									/>
								</div>
								<div className="mt-4 hidden overflow-x-auto md:block">
									<table className="w-full min-w-[700px] text-left text-xs">
										<thead>
											<tr className="border-b border-[var(--border-subtle)] text-[var(--text-secondary)]">
												<th className="pr-4 pb-3 font-medium">Configuration</th>
												<th className="pr-4 pb-3 font-medium">Runs</th>
												<th className="pr-4 pb-3 font-medium">Time / 1h</th>
												<th className="pr-4 pb-3 font-medium">GPU peak / avg</th>
												<th className="pb-3 font-medium">RAM peak / avg</th>
											</tr>
										</thead>
										<tbody>
											{groups.map((row, index) => (
												<tr
													key={index}
													className="border-b border-[var(--border-subtle)] last:border-0"
												>
													<td className="max-w-80 py-4 pr-4">
														<Configuration row={row} />
													</td>
													<td className="py-4 pr-4 align-top">
														<span className="font-semibold">{row.runs}</span>
														<span className="mt-1 block text-[10px] text-[var(--text-secondary)]">
															{row.completed} done · {row.failed} failed
														</span>
													</td>
													<td className="py-4 pr-4 align-top tabular-nums">
														<span className="font-semibold">
															{statisticsDuration(row.median_hour_seconds)}
														</span>
														<span className="mt-1 block text-[10px] text-[var(--text-secondary)]">
															{row.timing_measured_runs} timed
														</span>
													</td>
													<td className="py-4 pr-4 align-top whitespace-nowrap tabular-nums">
														{statisticsMemory(row.memory.peak_gpu_bytes)} /{' '}
														{statisticsMemory(row.memory.average_gpu_bytes)}
													</td>
													<td className="py-4 align-top whitespace-nowrap tabular-nums">
														{statisticsMemory(row.memory.peak_ram_bytes)} /{' '}
														{statisticsMemory(row.memory.average_ram_bytes)}
													</td>
												</tr>
											))}
										</tbody>
									</table>
								</div>
								<div className="mt-4 space-y-3 md:hidden">
									{groups.map((row, index) => (
										<article
											key={index}
											className="rounded-xl border border-[var(--border-subtle)] p-3"
										>
											<Configuration row={row} />
											<div className="mt-3 grid grid-cols-2 gap-3">
												<MemoryMetric
													label="Runs"
													value={String(row.runs)}
													note={`${row.completed} completed · ${row.failed} failed`}
												/>
												<MemoryMetric
													label="Time / 1h"
													value={statisticsDuration(row.median_hour_seconds)}
													note={`${row.timing_measured_runs} fresh timed`}
												/>
												<MemoryMetric
													label="GPU peak / avg"
													value={`${statisticsMemory(row.memory.peak_gpu_bytes)} / ${statisticsMemory(row.memory.average_gpu_bytes)}`}
												/>
												<MemoryMetric
													label="RAM peak / avg"
													value={`${statisticsMemory(row.memory.peak_ram_bytes)} / ${statisticsMemory(row.memory.average_ram_bytes)}`}
												/>
											</div>
										</article>
									))}
								</div>
								{groups.length === 0 && (
									<p className="py-6 text-center text-sm text-[var(--text-secondary)]">
										No matching models.
									</p>
								)}
							</section>
							{data.stages.length > 0 && (
								<section className={panel} aria-labelledby="statistics-stages">
									<h3
										id="statistics-stages"
										className="flex items-center gap-2 text-sm font-semibold"
									>
										<Clock3 className="h-4 w-4 text-[var(--brand-solid)]" />
										Stage timing
									</h3>
									<div className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
										{data.stages.map((stage) => (
											<div key={stage.kind} className="rounded-lg bg-[var(--bg-main)] p-3">
												<p className="text-xs font-medium">{statisticsStageLabel(stage.kind)}</p>
												<p className="mt-2 text-xl font-semibold tabular-nums">
													{statisticsDuration(stage.median_seconds)}
													<span className="ml-2 text-xs font-normal text-[var(--text-secondary)]">
														median attempt
													</span>
												</p>
												<p className="mt-1 text-[11px] text-[var(--text-secondary)]">
													{stage.attempts} attempts · {stage.retries} retries · {stage.failed}{' '}
													failed
												</p>
											</div>
										))}
									</div>
								</section>
							)}
							<details className={`${panel} text-xs text-[var(--text-secondary)]`}>
								<summary className="cursor-pointer font-medium text-[var(--text-primary)]">
									How these statistics are calculated
								</summary>
								<div className="mt-3 space-y-2 leading-relaxed">
									<p>
										These are local observations from retained execution history. No data is sent to
										an analytics service. Deleted recordings are excluded. The date filter uses the
										run start in UTC.
									</p>
									<p>
										Completion rate = completed / (completed + failed). Audio processed includes
										repeated completed runs and reused stages, where input duration was recorded.
									</p>
									<p>
										One-hour time is the median of run elapsed time / recorded input duration ×
										3,600. It excludes reused stages, paused/resumed executions and multi-track
										inputs. It is an estimate across your workloads, not an accuracy score or a
										controlled benchmark.
									</p>
									<p>
										Model rows separate the speaker checkpoint, recorded devices and transcription
										precision, and may include different quality settings. Device evidence comes
										from output metadata or successful stage attempts; requested settings never
										substitute for it.
									</p>
									<p>
										GPU memory covers the whole card, including other applications. Peak values use
										available run or stage samples. Averages use full-run sampled intervals only,
										weighted by their duration; pauses are excluded. Missing historical values stay
										blank.
									</p>
									<p>
										WER and DER need a reference transcript or speaker annotation and are not
										inferred from these runs.
									</p>
								</div>
								{data.failures.length > 0 && (
									<div className="mt-4 space-y-2">
										<h4 className="font-medium text-[var(--text-primary)]">
											Recorded failure codes
										</h4>
										{data.failures.map((f) => (
											<div
												key={`${f.stage}:${f.code}`}
												className="flex flex-wrap justify-between gap-2"
											>
												<span className="break-all">
													{statisticsStageLabel(f.stage)} · {f.code}
												</span>
												<span>{f.attempts} attempts</span>
											</div>
										))}
									</div>
								)}
							</details>
						</>
					)}
				</TabsContent>
				<TabsContent value="runs" className="min-w-0">
					<RunDiagnosticsSettings key={days} days={days} />
				</TabsContent>
			</Tabs>
		</section>
	);
}

function Configuration({ row }: { row: RunModelStatistics }) {
	return (
		<div className="min-w-0">
			<p className="text-sm font-medium break-words text-[var(--text-primary)]">
				{transcriptionModelLabel(row.model_family, row.model)}
			</p>
			<p className="mt-1 text-xs break-words text-[var(--text-secondary)]">
				{row.diarizer === 'none'
					? 'Speakers off'
					: row.diarizer === 'native'
						? 'Native speakers'
						: diarizationModelLabel(row.diarizer)}
			</p>
			<p className="mt-1 text-[11px] break-words text-[var(--text-tertiary)]">
				ASR {statisticsDevice(row.recognition_device)}
				{row.speaker_device !== 'none' && ` · speakers ${statisticsDevice(row.speaker_device)}`}
				{row.precision && ` · ${precisionLabel(row.precision)}`}
			</p>
			{(row.recovered > 0 || row.cpu_fallback > 0 || row.reused > 0) && (
				<p className="mt-1 text-[10px] text-violet-700 dark:text-violet-300">
					{[
						row.recovered ? `${row.recovered} recovered` : '',
						row.cpu_fallback ? `${row.cpu_fallback} CPU fallback` : '',
						row.reused ? `${row.reused} reused` : ''
					]
						.filter(Boolean)
						.join(' · ')}
				</p>
			)}
		</div>
	);
}

function Metric({
	icon,
	label,
	value,
	detail
}: {
	icon: ReactNode;
	label: string;
	value: string;
	detail: string;
}) {
	return (
		<div className={panel}>
			<div className="flex items-center gap-2 text-xs text-[var(--text-secondary)]">
				<span className="text-[var(--brand-solid)] [&_svg]:h-4 [&_svg]:w-4">{icon}</span>
				{label}
			</div>
			<p className="mt-3 text-2xl font-semibold tracking-tight tabular-nums sm:text-3xl">{value}</p>
			<p className="mt-2 text-[11px] text-[var(--text-secondary)]">{detail}</p>
		</div>
	);
}
function StatRow({ label, value }: { label: string; value: number }) {
	return (
		<div className="flex items-center justify-between gap-3 text-xs">
			<dt className="text-[var(--text-secondary)]">{label}</dt>
			<dd className="font-semibold tabular-nums">{value}</dd>
		</div>
	);
}
function MemoryMetric({ label, value, note }: { label: string; value: string; note?: string }) {
	return (
		<div className="min-w-0">
			<p className="text-[11px] text-[var(--text-secondary)]">{label}</p>
			<p className="mt-1 text-sm font-semibold break-words tabular-nums sm:text-base">{value}</p>
			{note && <p className="mt-1 text-[10px] text-[var(--text-tertiary)]">{note}</p>}
		</div>
	);
}

function ActivityChart({ data, days }: { data: RunStatistics; days: number }) {
	const rows = activityDays(data.activity, data.generated_at, days === 7 ? 7 : 30),
		max = Math.max(1, ...rows.map((r) => r.completed + r.failed + r.other));
	return (
		<section className={panel} aria-labelledby="statistics-activity">
			<div className="flex items-center justify-between gap-3">
				<h3 id="statistics-activity" className="text-sm font-semibold">
					Run activity
				</h3>
				<span className="text-[10px] text-[var(--text-tertiary)]">
					Last {rows.length} days · UTC
				</span>
			</div>
			<div
				className="mt-5 flex h-32 items-end gap-1 sm:gap-2"
				role="img"
				aria-label={`Daily run activity: ${rows.reduce((n, r) => n + r.completed + r.failed + r.other, 0)} runs in the last ${rows.length} days`}
			>
				{rows.map((row) => (
					<div
						key={row.date}
						className="flex h-full min-w-0 flex-1 flex-col justify-end"
						title={`${row.date}: ${row.completed} completed, ${row.failed} failed, ${row.other} other`}
					>
						<div
							className="min-h-0 rounded-t-sm bg-[var(--brand-solid)]/45"
							style={{ height: `${(row.other / max) * 100}%` }}
						/>
						<div
							className="min-h-0 bg-red-400"
							style={{ height: `${(row.failed / max) * 100}%` }}
						/>
						<div
							className="min-h-[2px] rounded-t-sm bg-emerald-500/80"
							style={{
								height: `${(row.completed / max) * 100}%`,
								opacity: row.completed + row.failed + row.other === 0 ? 0.15 : 1
							}}
						/>
					</div>
				))}
			</div>
			<div className="mt-2 flex justify-between text-[10px] text-[var(--text-tertiary)]">
				<span>{rows[0]?.date.slice(5)}</span>
				<span>{rows.at(-1)?.date.slice(5)}</span>
			</div>
			<div className="mt-4 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-[var(--text-secondary)]">
				<span>
					● <span className="text-emerald-600 dark:text-emerald-400">Completed</span>
				</span>
				<span>
					● <span className="text-red-500">Failed</span>
				</span>
				<span>● Other</span>
			</div>
		</section>
	);
}
