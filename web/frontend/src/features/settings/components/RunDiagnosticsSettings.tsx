import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import { ArrowLeft, ArrowUpRight, ChevronRight, Clock3, RefreshCw } from 'lucide-react';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { Button } from '@/components/ui/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useExecutionRuns, useRunLogs } from '@/features/transcription/hooks/useAudioDetail';
import { useExecutionRecovery } from '@/features/transcription/hooks/useExecutionRecovery';
import { useRunResources } from '@/features/transcription/hooks/useRunResources';
import { recoveryIsActive } from '@/features/transcription/hooks/recoveryPolicy';
import { transcriptionModelLabel } from '@/features/transcription/hooks/modelCapabilities';
import { RunResourcesPanel } from '@/features/transcription/components/audio-detail/RunResourcesPanel';
import { RunRecoveryPanel } from '@/features/transcription/components/audio-detail/RunRecoveryPanel';
import {
	RunSettingsPanel,
	RunTrackTimings
} from '@/features/transcription/components/audio-detail/RunSettingsPanel';
import { RunStatusBadge } from '@/features/transcription/components/audio-detail/RunPicker';
import { statisticsDuration } from '../hooks/runStatistics';
import { runDiagnosticsHref, type RunHistory } from '../hooks/runDiagnostics';

export function RunDiagnosticsSettings({ days }: { days: number }) {
	const [params, setParams] = useSearchParams();
	const recording = params.get('recording'),
		run = params.get('run');
	const [query, setQuery] = useState('');
	const [status, setStatus] = useState('all');
	const [page, setPage] = useState(1);
	const { getAuthHeaders } = useAuth();
	const hasSelection = !!recording && !!run;
	const history = useQuery({
		queryKey: ['run-history', days, query, status, page],
		enabled: !hasSelection,
		queryFn: async ({ signal }): Promise<RunHistory> => {
			const filters = new URLSearchParams({
				days: String(days),
				page: String(page),
				limit: '20',
				q: query,
				status
			});
			const response = await fetch(`/api/v1/transcription/statistics/runs?${filters}`, {
				headers: getAuthHeaders(),
				signal
			});
			if (!response.ok) throw new Error('Could not load run history.');
			return response.json();
		},
		staleTime: 15_000
	});
	if (hasSelection)
		return (
			<RunDiagnosticDetail
				key={`${recording}-${run}`}
				recordingID={recording}
				executionID={run}
				onBack={() => {
					const next = new URLSearchParams(params);
					next.delete('recording');
					next.delete('run');
					next.delete('section');
					setParams(next);
				}}
			/>
		);
	return (
		<section className="min-w-0 space-y-4" aria-label="Run diagnostics history">
			<div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 sm:flex sm:items-center">
				<input
					type="search"
					aria-label="Search run history"
					placeholder="Search recording, model or profile…"
					value={query}
					onChange={(e) => {
						setQuery(e.target.value);
						setPage(1);
					}}
					maxLength={200}
					className="col-span-2 h-11 min-w-0 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-main)] px-3 text-sm sm:flex-1"
				/>
				<select
					aria-label="Run history status"
					value={status}
					onChange={(e) => {
						setStatus(e.target.value);
						setPage(1);
					}}
					className="h-11 rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-main)] px-3 text-sm"
				>
					<option value="all">All statuses</option>
					<option value="completed">Completed</option>
					<option value="failed">Failed</option>
					<option value="active">Active</option>
				</select>
				<Button
					variant="outline"
					size="icon"
					aria-label="Refresh run history"
					className="h-11 w-11"
					disabled={history.isFetching}
					onClick={() => void history.refetch()}
				>
					<RefreshCw className="h-4 w-4" />
				</Button>
			</div>
			{history.isLoading && (
				<p role="status" className="py-8 text-center text-sm text-[var(--text-secondary)]">
					Loading run history…
				</p>
			)}
			{history.error && (
				<p role="alert" className="text-sm text-red-600">
					Could not load run history. Use Refresh to try again.
				</p>
			)}
			{history.data && (
				<>
					<p className="text-xs text-[var(--text-secondary)]">
						{history.data.pagination.total} run{history.data.pagination.total === 1 ? '' : 's'} · Select a run for resources, attempts, recovery
						and logs.
					</p>
					<div className="space-y-2">
						{history.data.runs.map((row) => (
							<Link
								key={row.id}
								to={runDiagnosticsHref(row.transcription_job_id, row.id)}
								className="flex min-w-0 items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] p-4 transition-colors hover:bg-[var(--bg-main)] focus-visible:outline-2 focus-visible:outline-[var(--brand-solid)]"
							>
								<div className="min-w-0 space-y-1.5">
									<div className="flex flex-wrap items-center gap-2">
										<span className="text-sm font-semibold break-words">{row.recording_title}</span>
										<span className="text-xs text-[var(--text-tertiary)]">
											Run {row.run_number}
										</span>
										<RunStatusBadge run={row} />
									</div>
									<p className="text-xs break-words text-[var(--text-secondary)]">
										{transcriptionModelLabel(row.model_family, row.model)}
										{row.profile_name && ` · ${row.profile_name}`}
									</p>
									<p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[var(--text-tertiary)]">
										<span>{new Date(row.started_at!).toLocaleString()}</span>
										<span className="inline-flex items-center gap-1">
											<Clock3 className="h-3 w-3" />
											{statisticsDuration(
												row.processing_duration == null ? null : row.processing_duration / 1000
											)}
										</span>
									</p>
								</div>
								<ChevronRight className="h-4 w-4 shrink-0 text-[var(--text-tertiary)]" />
							</Link>
						))}
					</div>
					{history.data.runs.length === 0 && (
						<p className="rounded-xl border border-[var(--border-subtle)] p-8 text-center text-sm text-[var(--text-secondary)]">
							No matching runs. Try a different filter or a longer period.
						</p>
					)}
					{history.data.pagination.pages > 1 && (
						<div className="flex items-center justify-between gap-3 text-xs">
							<Button variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>
								Previous
							</Button>
							<span>
								Page {page} of {history.data.pagination.pages}
							</span>
							<Button
								variant="outline"
								disabled={page >= history.data.pagination.pages}
								onClick={() => setPage(page + 1)}
							>
								Next
							</Button>
						</div>
					)}
				</>
			)}
		</section>
	);
}

function RunDiagnosticDetail({
	recordingID,
	executionID,
	onBack
}: {
	recordingID: string;
	executionID: string;
	onBack: () => void;
}) {
	const [params, setParams] = useSearchParams();
	const section = params.get('section') || 'resources';
	const activeSection = ['resources', 'recovery', 'settings', 'logs'].includes(section)
		? section
		: 'resources';
	const { getAuthHeaders } = useAuth();
	const identity = useQuery({
		queryKey: ['run-history-identity', executionID],
		queryFn: async ({ signal }): Promise<RunHistory> => {
			const response = await fetch(
				`/api/v1/transcription/statistics/runs?days=0&limit=1&run_id=${encodeURIComponent(executionID)}`,
				{ headers: getAuthHeaders(), signal }
			);
			if (!response.ok) throw new Error('Could not load run identity.');
			return response.json();
		}
	});
	const runs = useExecutionRuns(recordingID);
	const run = runs.data?.runs.find((row) => row.id === executionID);
	const running = recoveryIsActive(run?.status);
	const resources = useRunResources(recordingID, run?.id, activeSection === 'resources', running);
	const recovery = useExecutionRecovery(recordingID, run?.id, running);
	const logs = useRunLogs(recordingID, run?.id, activeSection === 'logs');
	const entry = identity.data?.runs.find((row) => row.transcription_job_id === recordingID);
	const refresh = () => {
		void identity.refetch();
		void runs.refetch();
		void recovery.refetch();
		if (activeSection === 'resources') void resources.refetch();
		if (activeSection === 'logs') void logs.refetch();
	};
	return (
		<section className="min-w-0 space-y-5" aria-label="Selected run diagnostics">
			<Button variant="ghost" size="sm" className="gap-2" onClick={onBack}>
				<ArrowLeft className="h-4 w-4" />
				All runs
			</Button>
			{runs.isLoading || identity.isLoading ? (
				<p role="status" className="text-sm">
					Loading run diagnostics…
				</p>
			) : runs.error || identity.error ? (
				<p role="alert" className="text-sm text-red-600">
					Could not load this run.{' '}
					<Button variant="outline" size="sm" onClick={refresh}>
						Try again
					</Button>
				</p>
			) : !run || !entry ? (
				<p role="alert" className="text-sm text-[var(--text-secondary)]">
					This run was not found for the recording. Choose another run from history.
				</p>
			) : (
				<>
					<div className="flex flex-wrap items-start justify-between gap-3">
						<div className="min-w-0">
							<p className="text-xs break-words text-[var(--text-secondary)]">
								{entry.recording_title}
							</p>
							<div className="mt-1 flex flex-wrap items-center gap-2">
								<h3 className="text-xl font-semibold">Run {run.run_number}</h3>
								<RunStatusBadge run={run} />
							</div>
							<p className="mt-2 text-xs text-[var(--text-secondary)]">
								{new Date(run.started_at!).toLocaleString()} ·{' '}
								{statisticsDuration(
									run.processing_duration == null ? null : run.processing_duration / 1000
								)}
							</p>
						</div>
						<div className="flex flex-wrap gap-2">
							<Button asChild variant="outline" size="sm" className="h-10 gap-2">
								<Link
									to={`/audio/${encodeURIComponent(recordingID)}?run=${encodeURIComponent(executionID)}`}
								>
									Open recording
									<ArrowUpRight className="h-4 w-4" />
								</Link>
							</Button>
							<Button
								variant="outline"
								size="icon"
								aria-label="Refresh selected run diagnostics"
								onClick={refresh}
							>
								<RefreshCw className="h-4 w-4" />
							</Button>
						</div>
					</div>
					{run.error_message && (
						<p
							role="alert"
							className="rounded-lg border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-600 dark:text-red-300"
						>
							{run.error_message}
						</p>
					)}
					<Tabs
						value={activeSection}
						onValueChange={(value) => {
							const next = new URLSearchParams(params);
							next.set('section', value);
							setParams(next, { replace: true });
						}}
						className="min-w-0 space-y-4"
					>
						<TabsList className="flex h-auto w-full justify-start overflow-x-auto bg-[var(--bg-main)]">
							<TabsTrigger value="resources" className="h-11 shrink-0 text-xs">
								Resources & stages
							</TabsTrigger>
							<TabsTrigger value="recovery" className="h-11 shrink-0 text-xs">
								Recovery
							</TabsTrigger>
							<TabsTrigger value="settings" className="h-11 shrink-0 text-xs">
								Settings
							</TabsTrigger>
							<TabsTrigger value="logs" className="h-11 shrink-0 text-xs">
								Logs
							</TabsTrigger>
						</TabsList>
						<TabsContent value="resources" className="min-w-0 space-y-4">
							<RunResourcesPanel
								resources={resources.data}
								loading={resources.isLoading}
								error={resources.error?.message}
								running={running}
							/>
							{run.multi_track_timings?.length ? (
								<RunTrackTimings timings={run.multi_track_timings} />
							) : null}
						</TabsContent>
						<TabsContent value="recovery" className="min-w-0">
							<RunRecoveryPanel
								executionID={run.id}
								parameters={run.actual_parameters}
								recovery={recovery.data}
								loading={recovery.isLoading}
								error={recovery.error instanceof Error ? recovery.error.message : undefined}
								onRetry={() => void recovery.refetch()}
								readOnly
								onSelectRun={(id) =>
									setParams(
										new URLSearchParams({
											tab: 'statistics',
											view: 'runs',
											recording: recordingID,
											run: id,
											section: 'recovery'
										})
									)
								}
							/>
						</TabsContent>
						<TabsContent value="settings" className="min-w-0">
							<RunSettingsPanel run={run} />
						</TabsContent>
						<TabsContent value="logs" className="min-w-0">
							{logs.isLoading ? (
								<p role="status">Loading logs…</p>
							) : logs.error ? (
								<p role="alert" className="text-sm text-red-600">
									Could not load run logs.
								</p>
							) : logs.data?.available ? (
								<pre className="max-h-[600px] overflow-auto rounded-xl bg-[var(--bg-main)] p-4 text-xs leading-5 break-words whitespace-pre-wrap">
									{logs.data.content}
								</pre>
							) : (
								<p className="text-sm text-[var(--text-secondary)]">No saved logs for this run.</p>
							)}
						</TabsContent>
					</Tabs>
				</>
			)}
		</section>
	);
}
