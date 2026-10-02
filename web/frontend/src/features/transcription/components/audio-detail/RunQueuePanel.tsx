import { useId, useState } from "react";
import { Link } from "react-router-dom";
import { ChevronDown, Clock3, ListOrdered, Loader2, Plus, RefreshCw, Square, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
    AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
    AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { cn } from "@/lib/utils";
import type { ExecutionRun } from "@/features/transcription/hooks/useAudioDetail";
import type { TranscriptionQueueItem } from "@/features/transcription/hooks/transcriptionQueue";
import { transcriptionModelLabel } from "@/features/transcription/hooks/modelCapabilities";
import {
    activityStateLabel, activityStageProgress, isCurrentQueueHead, occupiedWorkerRecordings, queuedActivityRuns,
    type QueueActivity, type QueueActivityEntry, type QueueWaitTarget,
} from "../../hooks/queueActivity";
import { useQueueActivity } from "../../hooks/useQueueActivity";

interface RunQueuePanelProps {
    recordingId: string;
    recordingTitle: string;
    items: TranscriptionQueueItem[];
    activeItem?: TranscriptionQueueItem | null;
    currentRun?: ExecutionRun;
    currentStatus?: "pending" | "processing";
    runInProgress: boolean;
    loading?: boolean;
    refreshing?: boolean;
    error?: string;
    queueBusy?: boolean;
    busyItemId?: string;
    announcement?: string;
    onAddRun: () => void;
    onRemoveRun: (runId: string) => void;
    onClearQueue: () => void;
    onStopRun: () => void;
    onRetry: () => void | Promise<unknown>;
}

type QueueRow = Pick<QueueActivityEntry,
    "recording_id" | "recording_title" | "model_family" | "model" | "state" |
    "profile_name" | "queue_item_id" | "execution_id" | "run_number" | "stage" | "stage_number" | "stage_total" | "stage_scope"
> & { has_worker?: boolean; waiting_on?: QueueWaitTarget[]; wait_reason?: 'worker' | 'earlier_run' };

export function RunQueuePanel({
    recordingId, recordingTitle, items, activeItem, currentRun,
    currentStatus = "processing", runInProgress, loading = false, refreshing = false,
    error, queueBusy = false, busyItemId, announcement,
    onAddRun, onRemoveRun, onClearQueue, onStopRun, onRetry,
}: RunQueuePanelProps) {
    const [showAll, setShowAll] = useState(false);
    const listId = useId();
    const query = useQueueActivity();
    const currentExecution = currentRun && ["pending", "processing", "running", "waiting", "waiting_for_resource"].includes(currentRun.status || "")
        && (!activeItem || activeItem.execution_id === currentRun.id) ? currentRun : undefined;
    const parameters = activeItem?.parameters ?? currentExecution?.actual_parameters;
    const localEntry: QueueActivityEntry = {
        recording_id: recordingId,
        recording_title: recordingTitle,
        state: runInProgress ? currentStatus === "pending" ? "waiting_worker" : "checking" : "queued",
        has_worker: false,
        queue_item_id: activeItem?.id,
        queued_at: activeItem?.queued_at,
        execution_id: activeItem?.execution_id ?? currentExecution?.id,
        run_number: currentExecution?.run_number,
        model_family: parameters?.model_family ?? "",
        model: parameters?.model ?? "",
        profile_name: activeItem?.profile_name ?? currentExecution?.profile_name,
        queued_runs: items.length,
        queued_jobs: items.map((item) => ({
            queue_item_id: item.id, queued_at: item.queued_at,
            model_family: item.parameters.model_family ?? "", model: item.parameters.model ?? "",
            profile_name: item.profile_name,
        })),
    };
    const localActivity: QueueActivity = {
        generated_at: "", workers: 0, busy_workers: 0, waiting_recordings: 0,
        queued_runs: items.length, recordings: runInProgress || items.length ? [localEntry] : [],
    };
    const activity = query.data ?? localActivity;
    const activeRuns = activity.recordings.filter((entry) => entry.has_worker || entry.state === "checking");
    const queuedRuns = queuedActivityRuns(activity);
    const visibleQueuedRuns = showAll ? queuedRuns : queuedRuns.slice(0, 2);
    const thisRecording = activity.recordings.find((entry) => entry.recording_id === recordingId);
    const blockers = !query.isError && thisRecording?.state === "waiting_worker"
        ? occupiedWorkerRecordings(activity, recordingId) : [];
    const controlsDisabled = queueBusy || refreshing || !!error || query.isError;
    const ownItems = new Map(items.map((item) => [item.id, item]));

    const renderRow = (row: QueueRow, kind: "active" | "queued") => {
        const ownItem = row.recording_id === recordingId && row.queue_item_id ? ownItems.get(row.queue_item_id) : undefined;
        const isCurrentHead = runInProgress && isCurrentQueueHead(row, recordingId, activeItem?.id, currentExecution?.id);
        return <RunRow
            key={row.queue_item_id ?? `${row.recording_id}:active`}
            row={row} kind={kind} currentRecordingId={recordingId}
            localItem={ownItem}
            disabled={controlsDisabled} busy={busyItemId === row.queue_item_id}
            canStop={isCurrentHead} onStopRun={onStopRun} onRemoveRun={onRemoveRun}
        />;
    };

    return <section id="recording-queue" aria-label="Run queue" className="glass-card overflow-hidden rounded-[var(--radius-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-card)]">
        <span className="sr-only" aria-live="polite">{announcement}</span>
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)] px-4 py-3">
            <div className="flex min-w-0 flex-wrap items-center gap-2">
                <h2 className="flex items-center gap-2 text-sm font-bold text-[var(--text-primary)]"><ListOrdered className="h-4 w-4 text-[var(--brand-solid)]" />Run queue</h2>
                {query.data && <span className="text-xs text-[var(--text-secondary)]">{activity.busy_workers} active · {queuedRuns.length} queued</span>}
            </div>
            <div className="flex shrink-0 items-center gap-1">
                {items.length > 0 && <AlertDialog>
                    <AlertDialogTrigger asChild><Button variant="ghost" size="icon" disabled={controlsDisabled} className="h-8 w-8 rounded-full text-[var(--text-secondary)]" aria-label={`Clear queued runs for ${recordingTitle}`} title="Clear this recording’s queued runs"><Trash2 className="h-4 w-4" /></Button></AlertDialogTrigger>
                    <AlertDialogContent>
                        <AlertDialogHeader><AlertDialogTitle>Clear this recording’s queued runs?</AlertDialogTitle>
                            <AlertDialogDescription>Cancel {items.length} queued {items.length === 1 ? "run" : "runs"} for “{recordingTitle}”? Its active run will continue. Cancelled runs cannot be restored.</AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter><AlertDialogCancel>Keep runs</AlertDialogCancel><AlertDialogAction onClick={onClearQueue} disabled={controlsDisabled} className="bg-red-600 text-white hover:bg-red-700">Cancel queued runs</AlertDialogAction></AlertDialogFooter>
                    </AlertDialogContent>
                </AlertDialog>}
                <Button variant="ghost" size="icon" className="h-8 w-8 rounded-full text-[var(--text-secondary)]" aria-label="Refresh run queue" disabled={query.isFetching || refreshing} onClick={() => void Promise.all([query.refetch(), onRetry()])}><RefreshCw className={cn("h-4 w-4", (query.isFetching || refreshing) && "animate-spin motion-reduce:animate-none")} /></Button>
                <Button size="sm" onClick={onAddRun} disabled={controlsDisabled} className="gap-1.5 rounded-full border-0 !text-white" style={{ background: "var(--brand-gradient)" }} title={`Add a run for ${recordingTitle}`}><Plus className="h-4 w-4" />Add run</Button>
            </div>
        </div>
        {blockers.length > 0 && <p className="border-b border-[var(--border-subtle)] bg-[var(--brand-light)]/35 px-4 py-2 text-sm text-[var(--text-primary)]">
            <Clock3 className="mr-1.5 inline h-4 w-4 align-text-bottom text-[var(--brand-solid)]" />
            <span className="font-semibold text-[var(--brand-solid)]">Waiting on: </span>
            {blockers.slice(0, 2).map((entry, index) => <span key={entry.recording_id}>
                {index > 0 && " and "}<Link to={runHref(entry)} className="font-semibold underline underline-offset-2">{entry.recording_title}</Link>
            </span>)}{blockers.length > 2 && ` and ${blockers.length - 2} more recordings`}
        </p>}
        {error && <div role="alert" className="flex flex-wrap items-center justify-between gap-2 px-4 py-2 text-sm text-[var(--error-solid)]"><span>{error}</span><Button variant="outline" size="sm" onClick={onRetry}>Try again</Button></div>}
        {query.isError && <p role="alert" className="px-4 py-2 text-xs text-[var(--error-solid)]">{query.data ? "Live queue updates are unavailable. Showing the last known runs." : "Shared queue status is unavailable. Showing this recording’s runs."}</p>}
        {(loading || query.isPending) && <p role="status" className="flex items-center gap-2 px-4 py-2 text-xs text-[var(--text-secondary)]"><Loader2 className="h-3 w-3 animate-spin motion-reduce:animate-none" />Refreshing queue…</p>}
        {activeRuns.length > 0 && <ul aria-label="Active runs" className="divide-y divide-[var(--border-subtle)] border-b border-[var(--border-subtle)]">{activeRuns.map((row) => renderRow(row, "active"))}</ul>}
        {queuedRuns.length > 0 ? <>
            <div className="px-4 pt-3 text-[10px] font-semibold uppercase tracking-wide text-[var(--text-tertiary)]">{showAll ? "Queued runs" : "Latest queued runs"}</div>
            <ul id={listId} aria-label="Queued runs" className="divide-y divide-[var(--border-subtle)]">{visibleQueuedRuns.map((row) => renderRow(row, "queued"))}</ul>
            {queuedRuns.length > 2 && <div className="border-t border-[var(--border-subtle)] px-3 py-2"><Button variant="ghost" size="sm" className="gap-2 rounded-full text-[var(--brand-solid)]" aria-controls={listId} aria-expanded={showAll} onClick={() => setShowAll(!showAll)}>{showAll ? "Show latest 2" : `Show all ${queuedRuns.length} queued runs`}<ChevronDown className={cn("h-4 w-4 transition-transform", showAll && "rotate-180")} /></Button></div>}
        </> : !loading && !query.isPending && <p className="px-4 py-3 text-sm text-[var(--text-secondary)]">{query.data ? "No queued runs." : "No queued runs for this recording."}</p>}
    </section>;
}

function RunRow({ row, kind, currentRecordingId, localItem, disabled, busy, canStop, onStopRun, onRemoveRun }: {
    row: QueueRow; kind: "active" | "queued"; currentRecordingId: string;
    localItem?: TranscriptionQueueItem;
    disabled: boolean; busy: boolean; canStop: boolean;
    onStopRun: () => void;
    onRemoveRun: (runId: string) => void;
}) {
    const status = row.state === "queued" ? "Queued" : activityStateLabel({ ...row, has_worker: !!row.has_worker, queued_runs: 0 });
    const stage = ['resource_wait', 'retry_wait'].includes(row.state) ? activityStageProgress({ ...row, has_worker: !!row.has_worker, queued_runs: 0 }) : undefined;
    const spinning = row.has_worker && !['resource_wait', 'retry_wait'].includes(row.state);
    return <li data-queue-kind={kind} className={cn("grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1 px-4 py-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_minmax(10rem,1fr)_6rem]", kind === "active" && "bg-[var(--brand-light)]/25")}>
        <div className="min-w-0">
            <Link to={runHref(row)} className="block break-words text-sm font-semibold leading-5 text-[var(--text-primary)] hover:underline">{row.recording_id === currentRecordingId && <span className="font-normal text-[var(--text-secondary)]">(This) · </span>}{row.recording_title}</Link>
        </div>
        <div className="col-start-1 row-start-2 min-w-0 lg:col-start-auto lg:row-start-auto">
            <p className="break-words text-sm leading-5 text-[var(--text-primary)]">{transcriptionModelLabel(row.model_family, row.model)}</p>
            {(row.profile_name || row.run_number) && <p className="break-words text-xs leading-5 text-[var(--text-secondary)]">{row.run_number ? `Run ${row.run_number}${row.profile_name ? " · " : ""}` : ""}{row.profile_name}</p>}
        </div>
        <div className="col-start-1 row-start-3 min-w-0 text-xs text-[var(--text-secondary)] lg:col-start-auto lg:row-start-auto">
            <div className="flex items-center gap-1.5">{spinning ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-[var(--brand-solid)] motion-reduce:animate-none" /> : <Clock3 className="h-3.5 w-3.5 shrink-0" />}<span className={cn("break-words", kind === "active" && "font-medium text-[var(--brand-solid)]")}>{status}</span></div>
            {stage && <p className="mt-1 break-words">{stage}</p>}
            {!!row.waiting_on?.length && <p className="mt-1 break-words leading-4">{row.wait_reason === 'earlier_run' ? 'Earlier run for: ' : 'Waiting on: '}{row.waiting_on.slice(0, 2).map((target, index) => <span key={target.recording_id}>{index > 0 && " and "}<Link to={runHref(target)} className="underline underline-offset-2">{target.recording_title}{target.run_number ? <span className="whitespace-nowrap"> · Run {target.run_number}</span> : null}</Link></span>)}{row.waiting_on.length > 2 && ` and ${row.waiting_on.length - 2} more`}</p>}
        </div>
        <div className="col-start-2 row-span-3 row-start-1 flex items-center justify-end gap-0.5 lg:col-start-auto lg:row-span-1 lg:row-start-auto">
            {canStop ? <Button variant="outline" size="sm" onClick={onStopRun} disabled={disabled} className="gap-1.5 rounded-full border-red-500/25 bg-red-500/5 px-2 text-xs text-[var(--error-solid)]"><Square className="h-3 w-3 fill-current" />{row.state === "waiting_worker" ? "Cancel run" : "Stop run"}</Button>
                : localItem && <Button variant="ghost" size="icon" disabled={disabled} onClick={() => onRemoveRun(localItem.id)} aria-label={`Cancel queued run for ${row.recording_title} using ${transcriptionModelLabel(row.model_family, row.model)}`} title="Cancel queued run" className="h-7 w-7 rounded-full text-[var(--text-secondary)] hover:text-[var(--error-solid)]">{busy ? <Loader2 className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" /> : <X className="h-3.5 w-3.5" />}</Button>}
        </div>
    </li>;
}

function runHref(row: Pick<QueueRow, "recording_id" | "execution_id">) {
    return `/audio/${encodeURIComponent(row.recording_id)}${row.execution_id ? `?run=${encodeURIComponent(row.execution_id)}` : "#recording-queue"}`;
}
