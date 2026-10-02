import { Button } from "@/components/ui/button";
import { canResumeExecution, partialRecoveryDownload, partialRecoveryText, recoveryAttemptErrorLabel, recoveryAttemptReasonLabel, recoveryMemoryRows, recoveryModeLabel, recoveryStageStatusLabel, stageLabel, type ExecutionRecovery, type RecoveryAttempt, type RecoveryParameters } from "@/features/transcription/hooks/recoveryPolicy";
import { adaptiveSettingsSummary, measuredMemory } from "@/features/transcription/hooks/adaptiveLearning";
import { ADAPTIVE_STAGE_LABELS, stagePolicy, type AdaptiveStageKind } from "@/features/transcription/hooks/adaptivePolicy";
import type { Transcript } from "@/features/transcription/hooks/useAudioDetail";
import { executionPolicySummary } from "@/features/transcription/hooks/executionPolicy";
import { Link } from "react-router-dom";
import { RunStageProgress } from "./RunStageProgress";

function AttemptSettings({ attempt }: { attempt: RecoveryAttempt }) {
    const settings = [
        attempt.device && `Device: ${attempt.device}`,
        attempt.precision && `Precision: ${attempt.precision}`,
        attempt.batch_size !== undefined && `Batch: ${attempt.batch_size}`,
        attempt.window_seconds !== undefined && `Window: ${attempt.window_seconds === 0 ? "model default / full recording" : `${attempt.window_seconds}s`}`,
        attempt.overlap_seconds !== undefined && `Overlap: ${attempt.overlap_seconds}s`,
        attempt.stitching_version && `Stitching: ${attempt.stitching_version}`,
        attempt.plan_version !== undefined && `Plan version: ${attempt.plan_version}`,
    ].filter(Boolean);
    return <p className="text-xs text-[var(--text-secondary)]">{settings.length ? settings.join(" · ") : "Effective settings not reported for this attempt."}</p>;
}

function AttemptMeasurements({ attempt }: { attempt: RecoveryAttempt }) {
    const measurement = attempt.measurements;
    if (!measurement) return <p className="text-xs text-[var(--text-secondary)]">No measurements recorded for this attempt.</p>;
    return <div className="space-y-1 text-xs text-[var(--text-secondary)]">
        <dl className="grid gap-x-4 gap-y-1 sm:grid-cols-2">{recoveryMemoryRows(attempt).map((row) => <div key={row.label} className="flex flex-wrap gap-x-1"><dt>{row.label}:</dt><dd>{measuredMemory(row.bytes)}</dd></div>)}</dl>
        <p>{measurement.samples} sample(s) · {measurement.elapsed_seconds.toFixed(1)}s elapsed · scope: {measurement.scope || "not recorded"}{measurement.external_contention ? " · external contention detected" : ""}</p>
        {measurement.ownership_unknown && <p>GPU process ownership unavailable. This sample is ineligible for capacity learning; ownership uncertainty alone does not establish external contention.</p>}
        <p>{attempt.device === "cuda" ? "Device-wide usage includes other processes. PyTorch allocator peaks describe this worker and differ from total VRAM use." : "Host availability can include other workloads and container limits; owned-process RSS is recorded separately."} A measured completion does not establish transcription accuracy.</p>
    </div>;
}

export function RunRecoveryPanel({ executionID, parameters, recovery, loading, error, resuming, otherRunActive, onResume, onNewSubmission, onSelectRun, onRetry, compact = false, readOnly = false, diagnosticsHref }: {
    executionID: string;
    parameters?: RecoveryParameters;
    transcript?: Transcript | null;
    recovery?: ExecutionRecovery | null;
    loading?: boolean;
    error?: string;
    resuming?: boolean;
    otherRunActive?: boolean;
    onResume?: (executionID: string) => void;
    onNewSubmission?: () => void;
    onSelectRun?: (executionID: string) => void;
    onRetry?: () => void;
    compact?: boolean;
    readOnly?: boolean;
    diagnosticsHref?: string;
}) {
    if (loading) return <p role="status" className="text-xs text-[var(--text-secondary)]">Loading saved stages and recovery status…</p>;
    if (error) return <div role="alert" className="flex items-center gap-3 text-sm text-[var(--warning-solid)]"><span>{error}</span><Button variant="outline" size="sm" onClick={onRetry}>Retry</Button></div>;
    if (!recovery || recovery.available === false) return <p className="text-xs text-[var(--text-secondary)]">Stage details were not recorded for this run.</p>;
    if (recovery.execution_id !== executionID) return null;
    const partialText = partialRecoveryText(recovery);
    const resumable = canResumeExecution(recovery, executionID, otherRunActive);
    const completed = recovery.stages.filter((stage) => stage.status === "succeeded").length;
    const downloadPartialText = () => {
        const download = partialRecoveryDownload(recovery, executionID);
        if (!download) return;
        const url = URL.createObjectURL(download.blob);
        const link = document.createElement("a");
        link.href = url;
        link.download = download.filename;
        document.body.appendChild(link);
        link.click();
        link.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    };
    if (compact) {
        const failedStage = recovery.stages.find((stage) => ["failed", "blocked", "interrupted"].includes(stage.status));
        const failedAttempt = failedStage?.attempts?.at(-1);
        return <section aria-label="Execution progress" className="min-w-0 space-y-3 border-t border-[var(--border-subtle)] pt-3">
            <RunStageProgress recovery={recovery} />
            {failedAttempt?.error_code && <p className="text-xs text-red-600 dark:text-red-300">{stageLabel(failedStage!)}: {recoveryAttemptErrorLabel(failedAttempt.error_code)}</p>}
            {(recovery.resumable || partialText !== undefined) && <div className="space-y-2">
                <p className="text-xs text-[var(--text-secondary)]">{partialText !== undefined ? "Partial transcript saved; this run is incomplete." : "Saved stages are available to resume."}</p>
                <div className="flex flex-wrap gap-2">
                    {recovery.resumable && onResume && <Button variant="outline" size="sm" className="h-10" onClick={() => onResume(executionID)} disabled={!resumable || resuming}>{resuming ? "Resuming…" : "Resume saved run"}</Button>}
                    {partialText !== undefined && <Button variant="outline" size="sm" className="h-10" onClick={downloadPartialText}>Download partial text</Button>}
                </div>
                {recovery.resumable && otherRunActive && <p className="text-xs text-[var(--text-secondary)]">Wait for the active run before resuming.</p>}
                {recovery.resume_unavailable_reason && <p className="text-xs text-[var(--warning-solid)]">{recovery.resume_unavailable_reason}</p>}
            </div>}
            {diagnosticsHref && <Link to={diagnosticsHref} className="inline-flex min-h-9 items-center text-xs text-[var(--brand-solid)] underline-offset-4 hover:underline">View run diagnostics in Statistics →</Link>}
        </section>;
    }
    return <section aria-label="Execution recovery" className="space-y-2 border-t border-[var(--border-subtle)] pt-2">
        <RunStageProgress recovery={recovery} />
        {(recovery.resumable || partialText !== undefined) && <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-xs text-[var(--text-secondary)]">{partialText !== undefined ? "Partial transcript saved; requested output is incomplete." : "Saved work is available for recovery."}</p>
            {!readOnly && recovery.resumable && onResume && <Button variant="outline" size="sm" onClick={() => onResume(executionID)} disabled={!resumable || resuming}>{resuming ? "Resuming…" : "Resume saved execution"}</Button>}
        </div>}
        <details open={readOnly ? true : undefined}>
        <summary className="cursor-pointer text-xs font-medium text-[var(--text-primary)]">
            Attempts &amp; recovery details <span className="ml-2 font-normal text-[var(--text-secondary)]">{completed}/{recovery.stages.length} stages complete</span>
        </summary>
        <div className="mt-3 space-y-3 rounded-lg bg-[var(--bg-main)]/40 p-3">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div>
                <h3 className="text-sm font-semibold text-[var(--text-primary)]">Stages and recovery</h3>
                <p className="mt-1 text-xs text-[var(--text-secondary)]">{recoveryModeLabel({ ...parameters, recovery_mode: recovery.mode ?? parameters?.recovery_mode })} · {recovery.status}</p>
                {partialText !== undefined && <p className="mt-2 text-sm text-[var(--text-primary)]">Text is available from a saved checkpoint. Requested output is still incomplete.</p>}
            </div>
            <div className="flex flex-wrap gap-2">
                {!readOnly && onNewSubmission && <Button variant="outline" size="sm" onClick={onNewSubmission}>New submission…</Button>}
            </div>
        </div>
        <p className="text-xs leading-5 text-[var(--text-secondary)]">Resume continues this saved plan and keeps completed stages. A new submission creates a separate run.</p>
        {recovery.resume_unavailable_reason && <p className="text-xs text-[var(--warning-solid)]">{recovery.resume_unavailable_reason}</p>}
        {recovery.resumable && otherRunActive && <p className="text-xs text-[var(--text-secondary)]">Wait for the active run to finish before resuming this execution.</p>}
        <details className="space-y-2 text-xs">
            <summary className="cursor-pointer font-medium text-[var(--text-primary)]">Requested permissions and learned starts</summary>
            <p className="text-[var(--text-secondary)]">Requested policy: {recoveryModeLabel({ ...parameters, recovery_mode: recovery.mode ?? parameters?.recovery_mode })}. Actual attempt settings are recorded below; they do not overwrite the requested profile.</p>
            {parameters?.execution_policy && <p className="text-[var(--text-secondary)]">Saved {parameters.execution_policy_source === "global" ? "shared" : "override"} policy: {executionPolicySummary(parameters.execution_policy)} · maximum delay {parameters.execution_policy.max_backoff_seconds}s.</p>}
            {parameters?.adaptive_policy?.stages && Object.keys(parameters.adaptive_policy.stages).filter((key) => key in ADAPTIVE_STAGE_LABELS).map((key) => {
                const kind = key as AdaptiveStageKind;
                const policy = stagePolicy(parameters.adaptive_policy, kind);
                return <div key={kind} className="text-[var(--text-secondary)]"><p>{ADAPTIVE_STAGE_LABELS[kind]}: device {policy.device_locked ? "locked" : "unlocked"}; CPU {policy.allow_cpu ? `permitted at ${policy.cpu_precision}` : "not permitted"}; minimum batch {policy.min_batch_size}; shorter windows {policy.allow_shorter_windows ? `${policy.window_candidates.join(", ")}s, floor ${policy.min_window_seconds}s, overlap ${policy.overlap_seconds}s` : "not permitted"}.</p>{policy.fixed && <p>Frozen stage settings: {adaptiveSettingsSummary(policy.fixed)}</p>}</div>;
            })}
            {recovery.learning?.plans?.length ? recovery.learning.plans.map((plan) => <div key={`${plan.scope_key}-${plan.plan_id}`} className="rounded-md border border-[var(--border-subtle)] p-2"><p>Saved learned candidate · {plan.stage_key} · profile revision {plan.profile_revision}, generation {plan.learning_generation}</p><p className="text-[var(--text-secondary)]">{adaptiveSettingsSummary(plan.settings)}</p></div>) : <p className="text-[var(--text-secondary)]">No learned candidate plan was saved for this execution.</p>}
        </details>
        <ol className="space-y-3">
            {(recovery.stages || []).map((stage) => <li key={stage.id} className="rounded-lg border border-[var(--border-subtle)] p-3">
                <div className="flex flex-wrap items-center justify-between gap-2"><span className="text-sm font-medium text-[var(--text-primary)]">{stageLabel(stage)}</span><span className="text-xs text-[var(--text-secondary)]">{recoveryStageStatusLabel(stage)}</span></div>
                <p className="mt-1 text-xs text-[var(--text-secondary)]">{stage.checkpoint_id ? "Durable checkpoint saved." : stage.recoverable_boundary ? "A checkpoint can be saved when this stage completes." : "No separately recoverable output inside this stage."}</p>
                {stage.reused_from_execution_id && <p className="mt-1 text-xs text-[var(--text-secondary)]">Reused output from {onSelectRun ? <button className="underline underline-offset-2" onClick={() => onSelectRun(stage.reused_from_execution_id!)}>run {stage.reused_from_execution_id.slice(0, 8)}</button> : `run ${stage.reused_from_execution_id.slice(0, 8)}`}. This is not a fresh inference measurement.</p>}
                {!!stage.attempts?.length && <details className="mt-2 text-xs" open={stage.status === "failed" || stage.status === "interrupted"}>
                    <summary className="cursor-pointer text-[var(--text-secondary)]">{stage.attempts.length} attempt{stage.attempts.length === 1 ? "" : "s"}</summary>
                    <ol className="mt-2 space-y-2">{stage.attempts.map((attempt) => <li key={attempt.id} className="space-y-1 border-l border-[var(--border-subtle)] pl-3">
                        <p className="text-[var(--text-primary)]">Attempt {attempt.attempt_number} · {attempt.status}</p>
                        <AttemptSettings attempt={attempt} />
                        <AttemptMeasurements attempt={attempt} />
                        {attempt.reason && <p className="text-[var(--text-secondary)]">{recoveryAttemptReasonLabel(attempt.reason)}</p>}
                        {(attempt.error_message || attempt.error_code) && <p className="text-[var(--warning-solid)]">{attempt.error_code ? recoveryAttemptErrorLabel(attempt.error_code) : attempt.error_message}</p>}
                    </li>)}</ol>
                </details>}
            </li>)}
        </ol>
        {partialText !== undefined && <details open className="space-y-2">
            <summary className="cursor-pointer text-sm font-medium text-[var(--text-primary)]">Partial transcript · retained output</summary>
            <p className="text-xs leading-5 text-[var(--text-secondary)]">This text may still need requested timestamps or speaker labels. It has not replaced a completed or pinned result.</p>
            <Button variant="outline" size="sm" onClick={downloadPartialText}>Download partial text</Button>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-[var(--bg-card)] p-3 font-sans text-sm leading-6 text-[var(--text-primary)]">{partialText || "The saved transcript contains no recognized speech."}</pre>
        </details>}
        <p className="text-xs leading-5 text-[var(--text-secondary)]">{recovery.learning?.reason || "Only qualified measured stages can inform learned starts. Reused output is not a new memory or quality measurement."}</p>
        </div>
        </details>
    </section>;
}
