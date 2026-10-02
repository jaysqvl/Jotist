import { SwitchField } from "./FormHelpers";
import { permitsCPURecovery, permitsWindowRecovery, recoveryModeLabel, shouldReuseCheckpoints, type RecoveryMode, type RecoveryParameters } from "@/features/transcription/hooks/recoveryPolicy";
import { ADAPTIVE_STAGE_LABELS, adaptiveLevel, effectiveStagePolicy, qualifiedBatches, qualifiedWindows, updateStagePolicy, type AdaptiveExecutionPolicy, type AdaptiveStageChoice, type AdaptiveStageKind, type AdaptiveStagePolicy } from "@/features/transcription/hooks/adaptivePolicy";
import { adaptiveSettingsSummary } from "@/features/transcription/hooks/adaptiveLearning";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { ExecutionPolicyControls } from "./ExecutionPolicyControls";
import { DEFAULT_EXECUTION_POLICY, executionPolicySummary, previewCheckpointReuse, sharedRecoveryMode, type ExecutionPolicy } from "@/features/transcription/hooks/executionPolicy";

export function CheckpointReuseField({ value, onChange }: { value?: boolean | null; onChange: (reuse: boolean) => void }) {
    return <SwitchField id="reuse-transcription-checkpoints" label="Reuse compatible completed stages" checked={shouldReuseCheckpoints(value)} onCheckedChange={onChange}
        description="Reuse requires the same recording and verified matching settings, runtime and model revisions. Turn off to compute fresh outputs. Saved checkpoints remain available for resume." />;
}

export function RecoveryPolicyFields({ params, stages, validationErrors, sharedPolicy, onSourceChange, onExecutionChange, onReuseChange, onPolicyChange }: {
    params: RecoveryParameters; stages: AdaptiveStageChoice[]; validationErrors: string[]; sharedPolicy: ExecutionPolicy | null;
    onSourceChange: (shared: boolean) => void; onExecutionChange: (policy: ExecutionPolicy) => void;
    onReuseChange: (reuse: boolean) => void;
    onPolicyChange: (policy: AdaptiveExecutionPolicy) => void;
}) {
    const shared = params.execution_policy_source !== "override";
    const reuse = previewCheckpointReuse(params, sharedPolicy ?? undefined);
    const mode = shared ? sharedRecoveryMode(sharedPolicy ?? DEFAULT_EXECUTION_POLICY) : params.execution_policy?.automatic_recovery === false ? "fixed" : params.recovery_mode;
    const level = adaptiveLevel(mode);
    return <div className="min-w-0 space-y-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-main)]/40 p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-semibold">Automatic recovery</h3>
            <span className="text-xs text-[var(--text-secondary)]">{shared ? "Global settings" : "Override"}</span>
        </div>
        <p className="text-xs text-[var(--text-secondary)]">{shared
            ? sharedPolicy ? executionPolicySummary(sharedPolicy) : "Global settings are saved with each new run when it is queued."
            : params.execution_policy ? executionPolicySummary(params.execution_policy, params.recovery_mode) : recoveryModeLabel(params)} · Reuse {reuse ? "on" : "off"}</p>
        <details>
            <summary className="cursor-pointer py-2 text-sm font-medium">Recovery override & stage constraints</summary>
            <div className="min-w-0 space-y-4 pt-3">
                <SwitchField id="use-shared-execution-policy" label="Use global recovery settings" checked={shared} onCheckedChange={onSourceChange} />
                {shared ? <p className="text-xs leading-5 text-[var(--text-secondary)]">Managed in Settings → Transcription → Automatic recovery. New runs take the latest defaults; queued work keeps its saved policy. Any explicit stage constraints below still apply.</p> : <>
                    {params.execution_policy && !params.execution_policy.recovery_strength && <p className="text-xs text-[var(--text-secondary)]">Saved policy: {recoveryModeLabel(params)}. Select a strength to update this override; existing runs keep their saved behavior.</p>}
                    <ExecutionPolicyControls policy={params.execution_policy ?? DEFAULT_EXECUTION_POLICY} savedMode={params.execution_policy ? params.recovery_mode : undefined} onChange={onExecutionChange} />
                </>}
                    {(Object.keys(ADAPTIVE_STAGE_LABELS) as AdaptiveStageKind[]).filter((kind) => params.adaptive_policy?.stages?.[kind]?.fixed).map((kind) => <div key={kind} className="space-y-2 rounded-lg border border-[var(--border-subtle)] p-3">
                        <p className="text-sm font-medium">Frozen settings · {ADAPTIVE_STAGE_LABELS[kind]}</p>
                        <p className="text-xs text-[var(--text-secondary)]">{adaptiveSettingsSummary(params.adaptive_policy?.stages?.[kind]?.fixed)}</p>
                        <Button type="button" size="sm" variant="outline" onClick={() => onPolicyChange(updateStagePolicy(params.adaptive_policy, kind, { fixed: undefined }))}>Remove frozen stage override</Button>
                    </div>)}
                    {level >= 2 && <details className="rounded-xl border border-[var(--border-subtle)]">
                        <summary className="cursor-pointer px-4 py-3 text-sm font-medium">Advanced stage constraints</summary>
                        <p className="px-3 pb-3 text-xs leading-5 text-[var(--text-secondary)]">Optional limits on the selected recovery strength. Stage separation and saving completed work happen automatically.</p>
                        <div className="space-y-4 p-3 pt-0">{stages.map((stage) => <StagePermissions key={stage.kind} stage={stage} mode={mode} policy={effectiveStagePolicy(params.adaptive_policy, stage, mode)} onChange={(patch) => onPolicyChange(updateStagePolicy({ ...params.adaptive_policy, learn: params.adaptive_policy?.learn === true, stages: { ...params.adaptive_policy?.stages, [stage.kind]: effectiveStagePolicy(params.adaptive_policy, stage, mode) } }, stage.kind, patch))} />)}</div>
                        {Object.keys(params.adaptive_policy?.stages ?? {}).length > 0 && <Button type="button" className="m-3 mt-0" variant="outline" size="sm" onClick={() => onPolicyChange({ ...params.adaptive_policy, learn: params.adaptive_policy?.learn === true, stages: {} })}>Reset stage constraints</Button>}
                    </details>}
                    {!shared && level > 0 && <SwitchField id="adaptive-learn-starts" label="Learn compatible starting plans" checked={params.adaptive_policy?.learn === true} onCheckedChange={(learn) => onPolicyChange({ ...params.adaptive_policy, learn })} description="Eligible measured attempts can inform later runs from this saved profile. Review, freeze or reset them in the profile’s Learning view." />}
                <CheckpointReuseField value={reuse} onChange={onReuseChange} />
            </div>
        </details>
        {!!validationErrors.length && <ul role="alert" className="space-y-1 text-sm text-[var(--warning-solid)]">{validationErrors.map((error) => <li key={error}>{error}</li>)}</ul>}
    </div>;
}

function PolicyCheckbox({ checked, disabled, label, onChange }: { checked: boolean; disabled?: boolean; label: string; onChange: (value: boolean) => void }) {
    return <label className={`flex min-h-11 items-center gap-2 text-sm ${disabled ? "text-[var(--text-tertiary)]" : "text-[var(--text-primary)]"}`}>
        <input type="checkbox" className="h-4 w-4 shrink-0" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />{label}
    </label>;
}

function StagePermissions({ stage, mode, policy, onChange }: { stage: AdaptiveStageChoice; mode?: RecoveryMode; policy: AdaptiveStagePolicy; onChange: (patch: Partial<AdaptiveStagePolicy>) => void }) {
    const batches = qualifiedBatches(stage.descriptor);
    const windows = qualifiedWindows(stage.descriptor);
    const cpuPrecisions = stage.descriptor?.device_precisions.cpu || [];
    const cpuAllowed = permitsCPURecovery(mode) && !policy.device_locked && cpuPrecisions.length > 0;
    const windowsAllowed = permitsWindowRecovery(mode) && windows.length > 0;
    return <fieldset className="space-y-3 rounded-lg border border-[var(--border-subtle)] p-4">
        <legend className="px-1 text-sm font-semibold text-[var(--text-primary)]">{stage.label}</legend>
        <p className="text-xs text-[var(--text-secondary)]">{stage.descriptor?.recoverable ? "This adapter exposes a saved stage boundary." : "This stage has no separately declared recoverable boundary; combined output can still be saved when supported."}</p>
        {stage.descriptor?.qualification_notes?.map((note) => <p key={note} className="text-xs text-[var(--text-secondary)]">{note}</p>)}
        <div className="grid gap-4 sm:grid-cols-2">
            <label className="space-y-1 text-sm text-[var(--text-primary)]"><span>Minimum batch size</span>
                <select className="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--bg-main)] p-2" value={policy.min_batch_size} disabled={!batches.length} onChange={(event) => onChange({ min_batch_size: Number(event.target.value) })}>
                    {!batches.includes(policy.min_batch_size) && <option value={policy.min_batch_size}>{policy.min_batch_size}{batches.length ? " (saved bound)" : " · batch adaptation unavailable"}</option>}
                    {batches.map((batch) => <option key={batch} value={batch}>{batch}</option>)}
                </select>
                <span className="block text-xs text-[var(--text-secondary)]">{batches.length ? `Qualified batches: ${batches.join(", ")}. Recovery never goes below this bound.` : "The adapter has not qualified smaller batches."}</span>
            </label>
            <div className="space-y-2">
                <PolicyCheckbox checked={policy.device_locked} label="Lock this stage to its selected device" onChange={(value) => onChange({ device_locked: value, ...(value ? { allow_cpu: false } : {}) })} />
                <PolicyCheckbox checked={policy.allow_cpu} disabled={!cpuAllowed && !policy.allow_cpu} label="Allow this stage to retry on CPU" onChange={(value) => onChange({ allow_cpu: value })} />
                <p className="text-xs text-[var(--text-secondary)]">{!permitsCPURecovery(mode) ? "Aggressive recovery permits CPU fallback." : policy.device_locked ? "Unlock the device to permit CPU fallback." : !cpuPrecisions.length ? "This model has no declared CPU precision options." : "CPU fallback uses the precision you select."}</p>
                <label className="block space-y-1 text-sm text-[var(--text-primary)]"><span>CPU fallback precision</span>
                    <select className="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--bg-main)] p-2" value={policy.cpu_precision} disabled={!cpuAllowed} onChange={(event) => onChange({ cpu_precision: event.target.value })}>
                        {!cpuPrecisions.includes(policy.cpu_precision) && <option value={policy.cpu_precision}>{policy.cpu_precision || "Choose explicitly"}{cpuPrecisions.length ? " (not supported)" : " · default, not enabled"}</option>}
                        {cpuPrecisions.map((precision) => <option key={precision} value={precision}>{precision}{precision === "float32" ? " · FP32" : ""}</option>)}
                    </select>
                </label>
            </div>
        </div>
        {stage.kind === "recognition" && <PolicyCheckbox checked={policy.allow_output_changes ?? policy.allow_shorter_windows} disabled={!permitsWindowRecovery(mode) && !(policy.allow_output_changes ?? policy.allow_shorter_windows)} label="Allow supported model timing repairs and decoder window splits" onChange={(value) => onChange({ allow_output_changes: value })} />}
        {windows.length > 0 && <PolicyCheckbox checked={policy.allow_shorter_windows} disabled={!windowsAllowed && !policy.allow_shorter_windows} label="Allow selected stage window sizes" onChange={(value) => onChange({ allow_shorter_windows: value })} />}
        <p className="text-xs text-[var(--text-secondary)]">{!permitsWindowRecovery(mode) ? "Strong or Aggressive recovery permits context changes." : !windows.length ? "This adapter has no configurable shorter-window candidates. Saved limits on context changes still apply to model-internal recovery." : stage.kind === "recognition" ? "Shorter recognition windows may change context, wording and accuracy." : "Only qualified window lengths and overlap policies can be used."}</p>
        {windows.length > 0 && <fieldset disabled={!windowsAllowed || !policy.allow_shorter_windows} className="space-y-3 disabled:opacity-50">
            <div className="flex flex-wrap gap-4">{windows.map((seconds) => <PolicyCheckbox key={seconds} checked={policy.window_candidates.includes(seconds)} disabled={!policy.window_candidates.includes(seconds) && policy.window_candidates.length >= 2} label={`${seconds} seconds`} onChange={(checked) => onChange({ window_candidates: checked ? [...policy.window_candidates, seconds].sort((a, b) => b - a) : policy.window_candidates.filter((value) => value !== seconds) })} />)}</div>
            <div className="grid gap-3 sm:grid-cols-2">
                <label className="text-sm text-[var(--text-primary)]">Minimum window (seconds)<Input type="number" min={1} step={1} value={policy.min_window_seconds} onChange={(event) => onChange({ min_window_seconds: Number(event.target.value) })} /></label>
                <label className="text-sm text-[var(--text-primary)]">Context overlap per side (seconds)<Input type="number" min={stage.descriptor?.window_policy?.minimum_overlap || 0} step={0.5} value={policy.overlap_seconds} onChange={(event) => onChange({ overlap_seconds: Number(event.target.value) })} /></label>
            </div>
            <p className="text-xs text-[var(--text-secondary)]">Select at most two windows. Overlap must be at least {stage.descriptor?.window_policy?.minimum_overlap}s and less than half the shortest selected window. Stitching: {stage.descriptor?.window_policy?.stitching_version}.</p>
        </fieldset>}
    </fieldset>;
}
