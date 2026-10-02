import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { BarChart3, Loader2, SlidersHorizontal } from 'lucide-react';
import {
	Dialog,
	DialogContent,
	DialogHeader,
	DialogTitle,
	DialogDescription
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { useExecutionRuns } from '../../hooks/useAudioDetail';
import { runDiagnosticsHref } from '@/features/settings/hooks/runDiagnostics';
import { RunPicker, RunStatusBadge } from './RunPicker';
import { RunSettingsPanel } from './RunSettingsPanel';

interface ExecutionInfoDialogProps {
	audioId: string;
	isOpen: boolean;
	onClose: (open: boolean) => void;
	initialRunId?: string;
}

export function ExecutionInfoDialog({
	audioId,
	isOpen,
	onClose,
	initialRunId
}: ExecutionInfoDialogProps) {
	const { data, isLoading, error } = useExecutionRuns(audioId, isOpen);
	const runs = useMemo(() => data?.runs || [], [data?.runs]);
	const [selectedRunId, setSelectedRunId] = useState<string>();
	useEffect(() => {
		if (isOpen) setSelectedRunId(initialRunId);
	}, [initialRunId, isOpen]);
	const selectedRun =
		runs.find((run) => run.id === selectedRunId) ||
		runs.find((run) => run.id === data?.active_run_id) ||
		runs[0];
	return (
		<Dialog open={isOpen} onOpenChange={onClose}>
			<DialogContent className="grid h-[min(680px,92dvh)] w-[95vw] grid-rows-[auto_minmax(0,1fr)] gap-0 overflow-hidden border-[var(--border-subtle)] bg-[var(--bg-card)] p-0 sm:max-w-2xl">
				<DialogHeader className="border-b border-[var(--border-subtle)] px-5 py-4">
					<DialogTitle className="flex items-center gap-2">
						<SlidersHorizontal className="h-5 w-5 text-[var(--brand-solid)]" />
						Run settings
					</DialogTitle>
					<DialogDescription>The configuration saved for this run.</DialogDescription>
				</DialogHeader>
				<div className="min-h-0 min-w-0 space-y-4 overflow-y-auto p-4 sm:p-5">
					{isLoading ? (
						<p role="status" className="flex items-center gap-2 text-sm">
							<Loader2 className="h-4 w-4 animate-spin" />
							Loading settings…
						</p>
					) : error ? (
						<p role="alert" className="text-sm text-red-600">
							Could not load run settings.
						</p>
					) : selectedRun ? (
						<>
							<RunPicker
								runs={runs}
								activeRunId={data?.active_run_id}
								pinnedRunId={data?.pinned_run_id}
								value={selectedRun.id}
								onValueChange={setSelectedRunId}
								label="Settings run"
							/>
							<div className="flex flex-wrap items-center justify-between gap-2 text-xs">
								<RunStatusBadge run={selectedRun} />
								{selectedRun.profile_name && (
									<span className="break-words text-[var(--text-secondary)]">
										{selectedRun.profile_name}
									</span>
								)}
							</div>
							{selectedRun.error_message && (
								<p
									role="alert"
									className="rounded-lg bg-red-500/10 p-3 text-sm text-red-600 dark:text-red-300"
								>
									{selectedRun.error_message}
								</p>
							)}
							<RunSettingsPanel run={selectedRun} />
							<Button asChild variant="outline" className="w-full gap-2">
								<Link to={runDiagnosticsHref(audioId, selectedRun.id)}>
									<BarChart3 className="h-4 w-4" />
									View diagnostics in Statistics
								</Link>
							</Button>
						</>
					) : (
						<p className="text-sm text-[var(--text-secondary)]">
							No saved runs for this recording.
						</p>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
