import { useEffect, useMemo, useRef, type ReactNode } from "react";
import { Activity, AlertCircle, ArrowDownCircle, BarChart3, Download, FileText, GitCompareArrows, Info, Loader2, MoreVertical, Pin, PinOff, Play, RefreshCw, ScrollText, SlidersHorizontal, StopCircle } from "lucide-react";
import { Link } from "react-router-dom";
import { runDiagnosticsHref } from "@/features/settings/hooks/runDiagnostics";
import { runChoicePresentation } from "@/features/transcription/hooks/runChoices";
import { Button } from "@/components/ui/button";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";
import type { WhisperXParams } from "@/features/transcription/types";
import type { ExecutionRun, Transcript } from "@/features/transcription/hooks/useAudioDetail";
import type { ExecutionRecovery } from "@/features/transcription/hooks/recoveryPolicy";
import { RunRecoveryPanel } from "./RunRecoveryPanel";
import { RunModelSummary } from "./RunModelSummary";
import { RunPicker } from "./RunPicker";
import { activeTimedSegmentIndex, transcriptDisplaySegments, transcriptSpeakerLabel } from "@/features/transcription/hooks/transcriptPresentation";

type RunWorkspaceMode = "transcript" | "compare";
type DownloadFormat = "srt" | "txt" | "json";
type DiffSide = "primary" | "compare";

interface TranscriptDiff {
    primaryChanged: Set<number>;
    compareChanged: Set<number>;
    primaryTotal: number;
    compareTotal: number;
    unchanged: number;
}

interface ComparePlayback {
    currentTime: number;
    followPlayback: boolean;
    onPlayFrom: (time: number) => void;
    speakerMappings: Record<string, string>;
}

interface RunWorkspaceProps {
    runs: ExecutionRun[];
    activeRunId?: string;
    pinnedRunId?: string;
    activeRunPinned?: boolean;
    selectedRunId?: string;
    compareRunId?: string;
    mode: RunWorkspaceMode;
    transcriptContent: ReactNode;
    playback: ComparePlayback;
    onFollowPlaybackChange: (enabled: boolean) => void;
    selectedTranscript?: Transcript | null;
    compareTranscript?: Transcript | null;
    selectedTranscriptLoading: boolean;
    compareTranscriptLoading: boolean;
    onSelectedRunChange: (runId: string) => void;
    onCompareRunChange: (runId: string) => void;
    onModeChange: (mode: RunWorkspaceMode) => void;
    onRunAgain: () => void;
    runAgainLabel?: string;
    onStopRun?: () => void;
    runAgainDisabled?: boolean;
    canStopRun?: boolean;
    stoppingRun?: boolean;
    showRunControl?: boolean;
    onOpenRunDetails: (runId?: string) => void;
    onOpenRunLogs: (runId?: string) => void;
    onDownloadRun: (run: ExecutionRun, format: DownloadFormat, transcript?: Transcript | null) => void;
    onSetActiveRun: (runId: string) => void;
    onClearActiveRun: () => void;
    activeRunUpdating?: boolean;
    recovery?: ExecutionRecovery | null;
    recoveryLoading?: boolean;
    recoveryError?: string;
    resuming?: boolean;
    recoveryOtherRunActive?: boolean;
    onResumeExecution?: (executionID: string) => void;
    onNewSubmission?: () => void;
    onRetryRecovery?: () => void;
}

export function RunWorkspace({
    runs,
    activeRunId,
    pinnedRunId,
    activeRunPinned = false,
    selectedRunId,
    compareRunId,
    mode,
    transcriptContent,
    playback,
    onFollowPlaybackChange,
    selectedTranscript,
    compareTranscript,
    selectedTranscriptLoading,
    compareTranscriptLoading,
    onSelectedRunChange,
    onCompareRunChange,
    onModeChange,
    onRunAgain,
    runAgainLabel = "Run Again",
    onStopRun,
    runAgainDisabled = false,
    canStopRun = false,
    stoppingRun = false,
    showRunControl = true,
    onOpenRunDetails,
    onOpenRunLogs,
    onDownloadRun,
    onSetActiveRun,
    onClearActiveRun,
    activeRunUpdating = false,
    recovery,
    recoveryLoading,
    recoveryError,
    resuming,
    recoveryOtherRunActive,
    onResumeExecution,
    onNewSubmission,
    onRetryRecovery,
}: RunWorkspaceProps) {
    const selectedRun = runs.find((run) => run.id === selectedRunId) || runs[0];
    const comparisonChoices = runs.filter((run) => run.id !== selectedRun?.id);
    const compareRun = comparisonChoices.find((run) => run.id === compareRunId) || comparisonChoices[0];
    const visibleMode = compareRun ? mode : "transcript";
    const transcriptDiff = useMemo(
        () => buildTranscriptDiff(selectedTranscript, compareTranscript),
        [compareTranscript, selectedTranscript]
    );

    if (runs.length === 0) {
        return (
            <>
              <section className="glass-card rounded-[var(--radius-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-card)] p-4 sm:p-5">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <div>
                        <h2 className="flex items-center gap-2 text-base font-bold text-[var(--text-primary)]">
                            <Activity className="h-4 w-4 text-[var(--brand-solid)]" />
                            Runs
                        </h2>
                        <p className="mt-1 text-sm text-[var(--text-secondary)]">
                            No run history exists for this file yet.
                        </p>
                    </div>
                    {showRunControl && (
                        <RunControl
                            onRunAgain={onRunAgain}
                            runAgainLabel={runAgainLabel}
                            onStopRun={onStopRun}
                            runAgainDisabled={runAgainDisabled}
                            canStopRun={canStopRun}
                            stoppingRun={stoppingRun}
                        />
                    )}
                </div>
              </section>
              {transcriptContent}
            </>
        );
    }

    return (
        <Tabs value={visibleMode} onValueChange={(value) => onModeChange(value as RunWorkspaceMode)} className="min-w-0 gap-4">
          <section aria-label="Run summary" className="glass-card rounded-[var(--radius-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-card)] p-3 sm:p-4">
            <div className="flex flex-col gap-3">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="flex items-center gap-2 text-base font-bold text-[var(--text-primary)]">
                            <Activity className="h-4 w-4 text-[var(--brand-solid)]" />
                            Runs
                            <span className="rounded-full bg-[var(--brand-light)] px-2 py-0.5 text-xs font-semibold text-[var(--brand-solid)]">
                                {runs.length}
                            </span>
                        </h2>
                    </div>
                    {showRunControl && <RunControl onRunAgain={onRunAgain} runAgainLabel={runAgainLabel} onStopRun={onStopRun} runAgainDisabled={runAgainDisabled} canStopRun={canStopRun} stoppingRun={stoppingRun} />}
                </div>
                <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-center">
                    <TabsList aria-label="Recording view" className="h-11 w-full sm:w-auto bg-[var(--bg-main)] border border-[var(--border-subtle)]">
                        <TabsTrigger value="transcript" className="flex-1 gap-2 sm:flex-none">
                            <ScrollText className="h-4 w-4" />
                            Transcript
                        </TabsTrigger>
                        <TabsTrigger value="compare" className="flex-1 gap-2 sm:flex-none" disabled={!compareRun}>
                            <GitCompareArrows className="h-4 w-4" />
                            Compare
                        </TabsTrigger>
                    </TabsList>
                    {visibleMode === "transcript" && (
                        <RunPicker
                            runs={runs}
                            activeRunId={activeRunId}
                            pinnedRunId={pinnedRunId}
                            value={selectedRun?.id}
                            onValueChange={onSelectedRunChange}
                            label="Primary run"
                        />
                    )}
                </div>

                {visibleMode === "transcript" && (
                    <SelectedRunPanel
                        run={selectedRun}
                        active={selectedRun?.id === activeRunId}
                        pinned={selectedRun?.id === pinnedRunId && activeRunPinned}
                        activeRunPinned={activeRunPinned}
                        transcript={selectedTranscript}
                        transcriptLoading={selectedTranscriptLoading}
                        onOpenRunDetails={onOpenRunDetails}
                        onOpenRunLogs={onOpenRunLogs}
                        onDownloadRun={onDownloadRun}
                        onSetActiveRun={onSetActiveRun}
                        onClearActiveRun={onClearActiveRun}
                        activeRunUpdating={activeRunUpdating}
                    />
                )}
                {selectedRun && onResumeExecution && <RunRecoveryPanel key={selectedRun.id} executionID={selectedRun.id}
                    compact diagnosticsHref={runDiagnosticsHref(selectedRun.transcription_job_id, selectedRun.id)}
                    parameters={selectedRun.actual_parameters} transcript={selectedTranscript}
                    recovery={recovery} loading={recoveryLoading} error={recoveryError} resuming={resuming}
                    otherRunActive={recoveryOtherRunActive} onResume={onResumeExecution}
                    onNewSubmission={onNewSubmission || onRunAgain} onSelectRun={onSelectedRunChange}
                    onRetry={onRetryRecovery || (() => undefined)} />}
            </div>
          </section>
          <TabsContent value="transcript">{transcriptContent}</TabsContent>
          <TabsContent value="compare" className="@container min-w-0 space-y-3">
              {compareRun && (
                  <>
                      <DiffSummary
                          diff={transcriptDiff}
                          loading={selectedTranscriptLoading || compareTranscriptLoading}
                          followPlayback={playback.followPlayback}
                          onFollowPlaybackChange={onFollowPlaybackChange}
                      />
                      <div className="grid grid-cols-1 items-start gap-4 @min-[42rem]:grid-cols-2">
                          <ComparePanel
                              title="Primary"
                              pinnedRunId={pinnedRunId}
                              runs={runs}
                              activeRunId={activeRunId}
                              run={selectedRun}
                              transcript={selectedTranscript}
                              loading={selectedTranscriptLoading}
                              selectedRunId={selectedRun?.id}
                              diff={transcriptDiff}
                              diffSide="primary"
                              playback={playback}
                              onRunChange={onSelectedRunChange}
                              onOpenRunDetails={onOpenRunDetails}
                              onOpenRunLogs={onOpenRunLogs}
                              onDownloadRun={onDownloadRun}
                          />
                          <ComparePanel
                              title="Comparison"
                              pinnedRunId={pinnedRunId}
                              runs={comparisonChoices}
                              activeRunId={activeRunId}
                              run={compareRun}
                              transcript={compareTranscript}
                              loading={compareTranscriptLoading || compareRun.id !== compareRunId}
                              selectedRunId={compareRun.id}
                              diff={transcriptDiff}
                              diffSide="compare"
                              playback={playback}
                              onRunChange={onCompareRunChange}
                              onOpenRunDetails={onOpenRunDetails}
                              onOpenRunLogs={onOpenRunLogs}
                              onDownloadRun={onDownloadRun}
                          />
                      </div>
                  </>
              )}
          </TabsContent>
        </Tabs>
    );
}

function RunControl({
    onRunAgain,
    runAgainLabel,
    onStopRun,
    runAgainDisabled,
    canStopRun,
    stoppingRun,
}: {
    onRunAgain: () => void;
    runAgainLabel: string;
    onStopRun?: () => void;
    runAgainDisabled: boolean;
    canStopRun: boolean;
    stoppingRun: boolean;
}) {
    if (canStopRun && onStopRun) {
        return (
            <Button
                variant="outline"
                size="sm"
                onClick={onStopRun}
                disabled={stoppingRun}
                className="h-11 gap-2 rounded-full border-red-500/30 bg-red-500/10 text-red-600 hover:bg-red-500/15 hover:text-red-700 dark:text-red-300 dark:hover:text-red-200 sm:h-8"
            >
                {stoppingRun ? (
                    <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                    <StopCircle className="h-4 w-4" />
                )}
                Stop Run
            </Button>
        );
    }

    return (
        <Button
            variant="outline"
            size="sm"
            onClick={onRunAgain}
            disabled={runAgainDisabled}
            className="h-11 gap-2 rounded-full border-[var(--border-subtle)] bg-[var(--bg-card)] sm:h-8"
        >
            <RefreshCw className="h-4 w-4" />
            {runAgainLabel}
        </Button>
    );
}

function SelectedRunPanel({
    run,
    active,
    pinned,
    activeRunPinned,
    transcript,
    transcriptLoading,
    onOpenRunDetails,
    onOpenRunLogs,
    onDownloadRun,
    onSetActiveRun,
    onClearActiveRun,
    activeRunUpdating,
}: {
    run?: ExecutionRun;
    active: boolean;
    pinned: boolean;
    activeRunPinned: boolean;
    transcript?: Transcript | null;
    transcriptLoading: boolean;
    onOpenRunDetails: (runId?: string) => void;
    onOpenRunLogs: (runId?: string) => void;
    onDownloadRun: (run: ExecutionRun, format: DownloadFormat, transcript?: Transcript | null) => void;
    onSetActiveRun: (runId: string) => void;
    onClearActiveRun: () => void;
    activeRunUpdating: boolean;
}) {
    if (!run) return null;
    const presentation = runChoicePresentation(run);

    return (
        <div className="space-y-3">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <div>
                        <div className="flex flex-wrap items-center gap-2">
                            {active && <ActiveBadge />}
                            {pinned && <PinnedBadge />}
                        </div>
                        <p className="mt-1 text-xs text-[var(--text-secondary)]">{presentation.speakers}{presentation.speakerRuntime && ` · ${presentation.speakerRuntime}`}</p>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                        <Button variant="ghost" size="sm" onClick={() => onOpenRunDetails(run.id)} className="h-11 gap-1.5 sm:h-8"><SlidersHorizontal className="h-3.5 w-3.5" />Settings</Button>
                        <ActiveRunButton
                            run={run}
                            active={active}
                            pinned={pinned}
                            activeRunPinned={activeRunPinned}
                            updating={activeRunUpdating}
                            onSetActiveRun={onSetActiveRun}
                            onClearActiveRun={onClearActiveRun}
                        />
                        <RunActions
                            compact
                            run={run}
                            transcript={transcript}
                            transcriptLoading={transcriptLoading}
                            onOpenRunDetails={onOpenRunDetails}
                            onOpenRunLogs={onOpenRunLogs}
                            onDownloadRun={onDownloadRun}
                        />
                    </div>
                </div>

                {run.error_message && (
                    <div className="flex gap-2 rounded-[var(--radius-card)] border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-600 dark:text-red-300">
                        <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                        <span>{run.error_message}</span>
                    </div>
                )}

        </div>
    );
}

function ActiveRunButton({
    run,
    active,
    pinned,
    activeRunPinned,
    updating,
    onSetActiveRun,
    onClearActiveRun,
}: {
    run: ExecutionRun;
    active: boolean;
    pinned: boolean;
    activeRunPinned: boolean;
    updating: boolean;
    onSetActiveRun: (runId: string) => void;
    onClearActiveRun: () => void;
}) {
    const canPin = run.status === "completed" && run.has_transcript !== false;

    if (pinned) {
        return (
            <Button
                variant="outline"
                size="sm"
                onClick={onClearActiveRun}
                disabled={updating}
                className="h-11 gap-2 rounded-full border-[var(--border-subtle)] bg-[var(--bg-card)] sm:h-8"
            >
                {updating ? <Loader2 className="h-4 w-4 animate-spin" /> : <PinOff className="h-4 w-4" />}
                Use Latest
            </Button>
        );
    }

    if (active && !activeRunPinned) {
        return (
            <Button
                variant="outline"
                size="sm"
                onClick={() => onSetActiveRun(run.id)}
                disabled={updating || !canPin}
                className="h-11 gap-2 rounded-full border-[var(--border-subtle)] bg-[var(--bg-card)] sm:h-8"
            >
                {updating ? <Loader2 className="h-4 w-4 animate-spin" /> : <Pin className="h-4 w-4" />}
                Pin Active
            </Button>
        );
    }

    return (
        <Button
            variant="outline"
            size="sm"
            onClick={() => onSetActiveRun(run.id)}
            disabled={updating || !canPin}
            className="h-11 gap-2 rounded-full border-[var(--border-subtle)] bg-[var(--bg-card)] sm:h-8"
        >
            {updating ? <Loader2 className="h-4 w-4 animate-spin" /> : <Pin className="h-4 w-4" />}
            Make Active
        </Button>
    );
}

function ComparePanel({
    title,
    runs,
    activeRunId,
    pinnedRunId,
    run,
    transcript,
    loading,
    selectedRunId,
    diff,
    diffSide,
    playback,
    onRunChange,
    onOpenRunDetails,
    onOpenRunLogs,
    onDownloadRun,
}: {
    title: string;
    runs: ExecutionRun[];
    activeRunId?: string;
    pinnedRunId?: string;
    run?: ExecutionRun;
    transcript?: Transcript | null;
    loading: boolean;
    selectedRunId?: string;
    diff: TranscriptDiff;
    diffSide: DiffSide;
    playback: ComparePlayback;
    onRunChange: (runId: string) => void;
    onOpenRunDetails: (runId?: string) => void;
    onOpenRunLogs: (runId?: string) => void;
    onDownloadRun: (run: ExecutionRun, format: DownloadFormat, transcript?: Transcript | null) => void;
}) {
    if (!run) {
        return (
            <div className="rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-main)]/60 p-4 text-sm text-[var(--text-tertiary)]">
                No second run available to compare.
            </div>
        );
    }

    const params = (run.actual_parameters || {}) as Partial<WhisperXParams>;

    return (
        <div className="min-w-0 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-main)]/50 p-3 sm:p-4">
            <div className="mb-3 flex items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                    <span className="text-xs font-semibold uppercase tracking-wide text-[var(--text-tertiary)]">{title}</span>
                    <RunPicker
                        runs={runs}
                        activeRunId={activeRunId}
                        pinnedRunId={pinnedRunId}
                        value={selectedRunId}
                        onValueChange={onRunChange}
                        label={`${title} run`}
                        compact
                    />
                </div>
                <RunActions
                    run={run}
                    transcript={transcript}
                    transcriptLoading={loading}
                    onOpenRunDetails={onOpenRunDetails}
                    onOpenRunLogs={onOpenRunLogs}
                    onDownloadRun={onDownloadRun}
                    compact
                />
            </div>
            <div className="mb-3"><RunModelSummary parameters={params} transcript={transcript} /></div>
            <TranscriptPreview label={`${title} transcript`} transcript={transcript} loading={loading} diff={diff} side={diffSide} playback={playback} />
        </div>
    );
}

function DiffSummary({ diff, loading, followPlayback, onFollowPlaybackChange }: {
    diff: TranscriptDiff;
    loading: boolean;
    followPlayback: boolean;
    onFollowPlaybackChange: (enabled: boolean) => void;
}) {
    const primaryChanged = diff.primaryChanged.size;
    const compareChanged = diff.compareChanged.size;
    const total = Math.max(diff.primaryTotal, diff.compareTotal);
    const changedPercent = total > 0 ? Math.round(((primaryChanged + compareChanged) / Math.max(1, diff.primaryTotal + diff.compareTotal)) * 100) : 0;

    return (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-main)]/50 p-3">
            <div className="flex flex-wrap items-center gap-2 text-xs">
                <span className="font-semibold uppercase tracking-wide text-[var(--text-tertiary)]">Diff</span>
                {loading ? (
                    <span className="text-[var(--text-secondary)]">Loading transcripts...</span>
                ) : total === 0 ? (
                    <span className="text-[var(--text-secondary)]">No transcript text available.</span>
                ) : (
                    <>
                        <DiffMetric label="Primary changes" value={primaryChanged} tone="removed" />
                        <DiffMetric label="Compare changes" value={compareChanged} tone="added" />
                        <DiffMetric label="Unchanged" value={diff.unchanged} />
                        <span className="rounded-full bg-[var(--bg-card)] px-2 py-1 font-mono text-[var(--text-secondary)]">
                            {changedPercent}% changed
                        </span>
                    </>
                )}
            </div>
            <Button
                type="button"
                size="sm"
                variant="outline"
                aria-pressed={followPlayback}
                onClick={() => onFollowPlaybackChange(!followPlayback)}
                className={cn("h-10 gap-2 sm:h-8", followPlayback && "border-[var(--brand-solid)]/40 bg-[var(--brand-light)] text-[var(--brand-solid)]")}
            >
                <ArrowDownCircle className="h-4 w-4" />
                Follow playback
            </Button>
        </div>
    );
}

function DiffMetric({ label, value, tone = "neutral" }: { label: string; value: number; tone?: "added" | "removed" | "neutral" }) {
    return (
        <span
            className={cn(
                "rounded-full px-2 py-1 font-mono",
                tone === "added" && "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
                tone === "removed" && "bg-red-500/10 text-red-700 dark:text-red-300",
                tone === "neutral" && "bg-[var(--bg-card)] text-[var(--text-secondary)]"
            )}
            title={label}
        >
            {label}: {value}
        </span>
    );
}

function TranscriptPreview({ label, transcript, loading, diff, side, playback }: {
    label: string;
    transcript?: Transcript | null;
    loading: boolean;
    diff: TranscriptDiff;
    side: DiffSide;
    playback: ComparePlayback;
}) {
    const paneRef = useRef<HTMLDivElement>(null);
    const { currentTime, followPlayback, onPlayFrom, speakerMappings } = playback;
    const changedWords = side === "primary" ? diff.primaryChanged : diff.compareChanged;
    const rows = useMemo(() => {
        const cursor = { current: 0 };
        return transcript ? transcriptDisplaySegments(transcript).map((segment) => ({
            ...segment,
            annotatedText: renderDiffTokens(segment.text, cursor, changedWords, side),
        })) : [];
    }, [transcript, changedWords, side]);
    const activeIndex = activeTimedSegmentIndex(rows, currentTime);

    useEffect(() => {
        const pane = paneRef.current;
        if (!followPlayback || loading || activeIndex < 0 || !pane) return;
        const row = pane.querySelector('[aria-current="true"]');
        if (!row) return;

        // Move this pane only. On a phone both panes share the page's outer scroll.
        const bounds = pane.getBoundingClientRect();
        const rowBounds = row.getBoundingClientRect();
        if (rowBounds.top < bounds.top || rowBounds.bottom > bounds.bottom) {
            pane.scrollTo({ top: pane.scrollTop + rowBounds.top - bounds.top - (pane.clientHeight - rowBounds.height) / 2 });
        }
    }, [activeIndex, followPlayback, loading, rows]);

    if (loading) {
        return <div className="rounded-[var(--radius-card)] bg-[var(--bg-card)] p-4 text-sm text-[var(--text-secondary)]">Loading transcript...</div>;
    }
    if (!transcript) {
        return <div className="rounded-[var(--radius-card)] bg-[var(--bg-card)] p-4 text-sm text-[var(--text-tertiary)]">No transcript captured for this run.</div>;
    }
    if (rows.length) {
        return (
            <div
                ref={paneRef}
                role="region"
                aria-label={label}
                tabIndex={0}
                className="max-h-[min(42dvh,26rem)] overflow-y-auto rounded-[var(--radius-card)] bg-[var(--bg-card)] p-2 outline-offset-2 md:max-h-[min(55dvh,32rem)]"
            >
                <div className="space-y-1">
                    {rows.map((segment, index) => (
                        <div
                            key={`${segment.start}-${index}`}
                            aria-current={index === activeIndex ? true : undefined}
                            className={cn(
                                "grid grid-cols-[4.6rem_minmax(0,1fr)] items-start gap-2 rounded-md border px-1.5 py-1.5 sm:grid-cols-[5.5rem_minmax(0,1fr)]",
                                index === activeIndex
                                    ? "border-[var(--brand-solid)]/35 bg-[var(--brand-light)]/60"
                                    : "border-transparent hover:bg-[var(--bg-main)]/70"
                            )}
                        >
                            <div className="min-w-0 select-none">
                                {Number.isFinite(segment.start) && segment.start >= 0 && (
                                    <button
                                        type="button"
                                        onClick={() => onPlayFrom(segment.start)}
                                        aria-label={`Play from ${formatTimestamp(segment.start)}`}
                                        className="inline-flex min-h-10 items-center gap-1 whitespace-nowrap rounded-md px-1 font-mono text-[11px] font-semibold tabular-nums text-[var(--text-primary)] hover:bg-[var(--bg-main)] focus-visible:outline-2 focus-visible:outline-[var(--brand-solid)] sm:min-h-7"
                                    >
                                        <Play className="size-3 shrink-0" aria-hidden="true" />
                                        {formatTimestamp(segment.start)}
                                    </button>
                                )}
                                {segment.speaker && (
                                    <div className="truncate px-1 text-[10px] font-semibold text-[var(--text-secondary)]" title={transcriptSpeakerLabel(segment.speaker, transcript, speakerMappings)}>
                                        {transcriptSpeakerLabel(segment.speaker, transcript, speakerMappings)}
                                    </div>
                                )}
                            </div>
                            <div className="min-w-0 wrap-break-word py-1 text-sm leading-6 text-[var(--text-primary)]">
                                {segment.annotatedText}
                            </div>
                        </div>
                    ))}
                </div>
            </div>
        );
    }

    return (
        <div role="region" aria-label={label} tabIndex={0} className="max-h-[min(42dvh,26rem)] overflow-y-auto rounded-[var(--radius-card)] bg-[var(--bg-card)] p-4 text-sm leading-7 text-[var(--text-primary)] whitespace-pre-wrap wrap-break-word md:max-h-[min(55dvh,32rem)]">
            {transcript.text ? renderDiffTokens(transcript.text, { current: 0 }, changedWords, side) : "Transcript is empty."}
        </div>
    );
}

function RunActions({
    run,
    transcript,
    transcriptLoading,
    onOpenRunDetails,
    onOpenRunLogs,
    onDownloadRun,
    compact = false,
}: {
    run: ExecutionRun;
    transcript?: Transcript | null;
    transcriptLoading: boolean;
    onOpenRunDetails: (runId?: string) => void;
    onOpenRunLogs: (runId?: string) => void;
    onDownloadRun: (run: ExecutionRun, format: DownloadFormat, transcript?: Transcript | null) => void;
    compact?: boolean;
}) {
    const downloadsDisabled = transcriptLoading || !transcript;

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button
                    variant="outline"
                    size={compact ? "icon" : "sm"}
                    aria-label={compact ? `Actions for run ${run.run_number}` : undefined}
                    className={cn(
                        "border-[var(--border-subtle)] bg-[var(--bg-card)]",
                        compact ? "h-11 w-11 rounded-full sm:h-8 sm:w-8" : "h-11 gap-2 rounded-full sm:h-8"
                    )}
                >
                    <MoreVertical className="h-4 w-4" />
                    {!compact && <span>Run Actions</span>}
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-52 glass-card rounded-[var(--radius-card)] border-[var(--border-subtle)] p-1.5 shadow-[var(--shadow-float)]">
                <DropdownMenuItem onClick={() => onOpenRunDetails(run.id)} className="rounded-[8px] cursor-pointer">
                    <Info className="mr-2 h-4 w-4 opacity-70" />
                    Run settings
                </DropdownMenuItem>
                <DropdownMenuItem asChild className="rounded-[8px] cursor-pointer"><Link to={runDiagnosticsHref(run.transcription_job_id, run.id)}><BarChart3 className="mr-2 h-4 w-4 opacity-70" />Diagnostics in Statistics</Link></DropdownMenuItem>
                <DropdownMenuItem onClick={() => onOpenRunLogs(run.id)} disabled={!run.has_logs} className="rounded-[8px] cursor-pointer">
                    <FileText className="mr-2 h-4 w-4 opacity-70" />
                    Logs
                </DropdownMenuItem>
                <DropdownMenuSeparator className="bg-[var(--border-subtle)] my-1" />
                <DropdownMenuItem onClick={() => onDownloadRun(run, "srt", transcript)} disabled={downloadsDisabled} className="rounded-[8px] cursor-pointer">
                    <Download className="mr-2 h-4 w-4 opacity-70" />
                    Download SRT
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => onDownloadRun(run, "txt", transcript)} disabled={downloadsDisabled} className="rounded-[8px] cursor-pointer">
                    <Download className="mr-2 h-4 w-4 opacity-70" />
                    Download Text
                </DropdownMenuItem>
                <DropdownMenuItem onClick={() => onDownloadRun(run, "json", transcript)} disabled={downloadsDisabled} className="rounded-[8px] cursor-pointer">
                    <Download className="mr-2 h-4 w-4 opacity-70" />
                    Download JSON
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    );
}

function buildTranscriptDiff(primary?: Transcript | null, compare?: Transcript | null): TranscriptDiff {
    const primaryTokens = tokenizeForDiff(getDiffText(primary));
    const compareTokens = tokenizeForDiff(getDiffText(compare));
    const primaryChanged = new Set<number>();
    const compareChanged = new Set<number>();
    let unchanged = 0;
    let primaryIndex = 0;
    let compareIndex = 0;
    const lookahead = 16;

    while (primaryIndex < primaryTokens.length && compareIndex < compareTokens.length) {
        if (primaryTokens[primaryIndex].normalized === compareTokens[compareIndex].normalized) {
            unchanged += 1;
            primaryIndex += 1;
            compareIndex += 1;
            continue;
        }

        const nextMatch = findNearbyTokenMatch(primaryTokens, compareTokens, primaryIndex, compareIndex, lookahead);
        if (nextMatch) {
            for (let i = primaryIndex; i < primaryIndex + nextMatch.primaryOffset; i += 1) {
                primaryChanged.add(i);
            }
            for (let i = compareIndex; i < compareIndex + nextMatch.compareOffset; i += 1) {
                compareChanged.add(i);
            }
            primaryIndex += nextMatch.primaryOffset;
            compareIndex += nextMatch.compareOffset;
            continue;
        }

        primaryChanged.add(primaryIndex);
        compareChanged.add(compareIndex);
        primaryIndex += 1;
        compareIndex += 1;
    }

    for (let i = primaryIndex; i < primaryTokens.length; i += 1) {
        primaryChanged.add(i);
    }
    for (let i = compareIndex; i < compareTokens.length; i += 1) {
        compareChanged.add(i);
    }

    return {
        primaryChanged,
        compareChanged,
        primaryTotal: primaryTokens.length,
        compareTotal: compareTokens.length,
        unchanged,
    };
}

function findNearbyTokenMatch(
    primaryTokens: DiffToken[],
    compareTokens: DiffToken[],
    primaryIndex: number,
    compareIndex: number,
    lookahead: number,
) {
    let best: { primaryOffset: number; compareOffset: number; cost: number } | null = null;
    const primaryMax = Math.min(primaryTokens.length - primaryIndex - 1, lookahead);
    const compareMax = Math.min(compareTokens.length - compareIndex - 1, lookahead);

    for (let primaryOffset = 0; primaryOffset <= primaryMax; primaryOffset += 1) {
        for (let compareOffset = 0; compareOffset <= compareMax; compareOffset += 1) {
            if (primaryOffset === 0 && compareOffset === 0) continue;
            if (primaryTokens[primaryIndex + primaryOffset].normalized !== compareTokens[compareIndex + compareOffset].normalized) continue;

            const cost = primaryOffset + compareOffset;
            if (!best || cost < best.cost) {
                best = { primaryOffset, compareOffset, cost };
            }
        }
    }

    return best;
}

interface DiffToken {
    raw: string;
    normalized: string;
}

function tokenizeForDiff(text: string): DiffToken[] {
    return (text.match(/\S+/g) || []).map((raw) => ({
        raw,
        normalized: normalizeDiffToken(raw),
    }));
}

function normalizeDiffToken(token: string) {
    const normalized = token
        .toLowerCase()
        .replace(/[“”]/g, '"')
        .replace(/[‘’]/g, "'")
        .replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, "");

    return normalized || token.toLowerCase();
}

function getDiffText(transcript?: Transcript | null) {
    if (!transcript) return "";
    if (transcript.segments?.length) {
        return transcript.segments.map((segment) => segment.text.trim()).filter(Boolean).join(" ");
    }
    return transcript.text || "";
}

function renderDiffTokens(text: string, cursor: { current: number }, changedWords: Set<number>, side: DiffSide) {
    const parts = text.match(/\s+|\S+/g) || [];
    return parts.map((part, index) => {
        if (/^\s+$/.test(part)) return part;

        const tokenIndex = cursor.current;
        cursor.current += 1;
        const changed = changedWords.has(tokenIndex);
        if (!changed) return part;

        return (
            <mark
                key={`${tokenIndex}-${index}`}
                className={cn(
                    "rounded px-0.5 text-[var(--text-primary)]",
                    side === "primary" && "bg-red-500/20 decoration-red-500/80 line-through decoration-2",
                    side === "compare" && "bg-emerald-500/20 underline decoration-emerald-500/80 decoration-2 underline-offset-2"
                )}
            >
                {part}
            </mark>
        );
    });
}

function formatTimestamp(seconds: number) {
    if (!Number.isFinite(seconds) || seconds < 0) return "00:00";
    const totalSeconds = Math.floor(seconds);
    const hours = Math.floor(totalSeconds / 3600);
    const minutes = Math.floor((totalSeconds % 3600) / 60);
    const remainingSeconds = totalSeconds % 60;

    if (hours > 0) {
        return `${hours}:${minutes.toString().padStart(2, "0")}:${remainingSeconds.toString().padStart(2, "0")}`;
    }

    return `${minutes.toString().padStart(2, "0")}:${remainingSeconds.toString().padStart(2, "0")}`;
}

function ActiveBadge() {
    return (
        <span className="rounded-full bg-[var(--brand-light)] px-2 py-0.5 text-[10px] font-semibold uppercase text-[var(--brand-solid)]">
            Current transcript
        </span>
    );
}

function PinnedBadge() {
    return (
        <span className="rounded-full bg-amber-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase text-amber-700 dark:text-amber-300">
            Pinned
        </span>
    );
}
