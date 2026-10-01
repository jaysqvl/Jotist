import { SelectField, SwitchField } from "./FormHelpers";
import { recoveryModeLabel, shouldReuseCheckpoints, type RecoveryMode, type RecoveryParameters } from "@/features/transcription/hooks/recoveryPolicy";
import { ADAPTIVE_STAGE_LABELS, adaptiveLevel, qualifiedBatches, qualifiedWindows, stagePolicy, updateStagePolicy, type AdaptiveExecutionPolicy, type AdaptiveStageChoice, type AdaptiveStageKind, type AdaptiveStagePolicy } from "@/features/transcription/hooks/adaptivePolicy";
import { adaptiveSettingsSummary } from "@/features/transcription/hooks/adaptiveLearning";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { ExecutionPolicyControls } from "./ExecutionPolicyControls";
import { executionPolicySummary, previewCheckpointReuse, type ExecutionPolicy } from "@/features/transcription/hooks/executionPolicy";

export function CheckpointReuseField({ value, onChange }: { value?: boolean | null; onChange: (reuse: boolean) => void }) {
    return <SwitchField id="reuse-transcription-checkpoints" label="Reuse compatible completed stages" checked={shouldReuseCheckpoints(value)} onCheckedChange={onChange}
        description="Reuse requires the same recording and verified matching settings, runtime and model revisions. Turn off to compute fresh outputs. Saved checkpoints remain available for resume." />;
}

export function RecoveryPolicyFields({ params, stages, validationErrors, sharedPolicy, onSourceChange, onExecutionChange, onModeChange, onReuseChange, onPolicyChange }: {
    params: RecoveryParameters; stages: AdaptiveStageChoice[]; validationErrors: string[]; sharedPolicy: ExecutionPolicy | null;
    onSourceChange: (shared: boolean) => void; onExecutionChange: (policy: ExecutionPolicy) => void;
    onModeChange: (mode: RecoveryMode) => void; onReuseChange: (reuse: boolean) => void;
    onPolicyChange: (policy: AdaptiveExecutionPolicy) => void;
}) {
    const shared = params.execution_policy_source === "global";
    const level = adaptiveLevel(params.recovery_mode);
    return <div className="min-w-0 space-y-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-main)]/40 p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-semibold">Recovery & execution</h3>
            <span className="text-xs text-[var(--text-secondary)]">{shared ? "Shared defaults" : "Saved override"}</span>
        </div>
        <p className="text-xs text-[var(--text-secondary)]">{shared
            ? sharedPolicy ? executionPolicySummary(sharedPolicy) : "Shared settings are resolved when the run starts."
            : params.execution_policy ? executionPolicySummary(params.execution_policy) : recoveryModeLabel(params)}</p>
        <details>
            <summary className="cursor-pointer py-2 text-sm font-medium">Recovery overrides</summary>
            <div className="min-w-0 space-y-4 pt-3">
                <SwitchField id="use-shared-execution-policy" label="Use shared execution defaults" checked={shared} onCheckedChange={onSourceChange} />
                {shared ? <p className="text-xs leading-5 text-[var(--text-secondary)]">Managed in Settings → Transcription → Recovery & execution. New runs take the latest defaults; queued work keeps its saved policy.</p> : <>
                    {params.execution_policy && <ExecutionPolicyControls policy={params.execution_policy} onChange={onExecutionChange} showBatchOption={false} />}
                    <SelectField label="Recovery actions" value={params.recovery_mode || "legacy"} onValueChange={(value) => onModeChange(value === "legacy" ? "" : value as RecoveryMode)} options={[
                        { value: "legacy", label: "Keep legacy device behavior" },
                        { value: "fixed", label: "Off · keep exact settings" },
                        { value: "stage_management", label: "Retry with the same settings" },
                        { value: "batch_management", label: "Retry and allow smaller batches" },
                        { value: "cpu_fallback", label: "Also allow explicit CPU fallback" },
                        { value: "shorter_windows", label: "Also allow selected shorter windows" },
                    ]} />
                    {!params.recovery_mode && <p className="text-xs text-[var(--text-secondary)]">This existing profile keeps its legacy Auto behavior. Choose shared defaults to use the unified retry controls.</p>}
                    {(Object.keys(ADAPTIVE_STAGE_LABELS) as AdaptiveStageKind[]).filter((kind) => params.adaptive_policy?.stages?.[kind]?.fixed).map((kind) => <div key={kind} className="space-y-2 rounded-lg border border-[var(--border-subtle)] p-3">
                        <p className="text-sm font-medium">Frozen settings · {ADAPTIVE_STAGE_LABELS[kind]}</p>
                        <p className="text-xs text-[var(--text-secondary)]">{adaptiveSettingsSummary(params.adaptive_policy?.stages?.[kind]?.fixed)}</p>
                        <Button type="button" size="sm" variant="outline" onClick={() => onPolicyChange(updateStagePolicy(params.adaptive_policy, kind, { fixed: undefined }))}>Remove frozen stage override</Button>
                    </div>)}
                    {level >= 2 && <details className="rounded-xl border border-[var(--border-subtle)]">
                        <summary className="cursor-pointer px-4 py-3 text-sm font-medium">Stage permissions</summary>
                        <div className="space-y-4 p-3 pt-0">{stages.map((stage) => <StagePermissions key={stage.kind} stage={stage} level={level} policy={stagePolicy(params.adaptive_policy, stage.kind)} onChange={(patch) => onPolicyChange(updateStagePolicy(params.adaptive_policy, stage.kind, patch))} />)}</div>
                    </details>}
                    {level > 0 && <SwitchField id="adaptive-learn-starts" label="Learn compatible starting plans" checked={params.adaptive_policy?.learn === true} onCheckedChange={(learn) => onPolicyChange({ ...params.adaptive_policy, learn })} description="Eligible measured attempts can inform later runs from this saved profile. Review, freeze or reset them in the profile’s Learning view." />}
                </>}
                <CheckpointReuseField value={previewCheckpointReuse(params, sharedPolicy ?? undefined)} onChange={onReuseChange} />
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

function StagePermissions({ stage, level, policy, onChange }: { stage: AdaptiveStageChoice; level: number; policy: AdaptiveStagePolicy; onChange: (patch: Partial<AdaptiveStagePolicy>) => void }) {
    const batches = qualifiedBatches(stage.descriptor);
    const windows = qualifiedWindows(stage.descriptor);
    const cpuPrecisions = stage.descriptor?.device_precisions.cpu || [];
    const cpuAllowed = level >= 3 && !policy.device_locked && cpuPrecisions.length > 0;
    const windowsAllowed = level >= 4 && windows.length > 0;
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
                <p className="text-xs text-[var(--text-secondary)]">{level < 3 ? "Choose a recovery action that allows CPU fallback." : policy.device_locked ? "Unlock the device to allow an explicit CPU fallback." : !cpuPrecisions.length ? "This model has no declared CPU precision options." : "CPU fallback keeps the model and context, using the precision you select."}</p>
                <label className="block space-y-1 text-sm text-[var(--text-primary)]"><span>CPU fallback precision</span>
                    <select className="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--bg-main)] p-2" value={policy.cpu_precision} disabled={!cpuAllowed} onChange={(event) => onChange({ cpu_precision: event.target.value })}>
                        {!cpuPrecisions.includes(policy.cpu_precision) && <option value={policy.cpu_precision}>{policy.cpu_precision || "Choose explicitly"}{cpuPrecisions.length ? " (not supported)" : " · default, not enabled"}</option>}
                        {cpuPrecisions.map((precision) => <option key={precision} value={precision}>{precision}{precision === "float32" ? " · FP32" : ""}</option>)}
                    </select>
                </label>
            </div>
        </div>
        <PolicyCheckbox checked={policy.allow_shorter_windows} disabled={!windowsAllowed && !policy.allow_shorter_windows} label="Allow explicitly selected shorter windows" onChange={(value) => onChange({ allow_shorter_windows: value })} />
        <p className="text-xs text-[var(--text-secondary)]">{level < 4 ? "Choose a recovery action that allows shorter windows." : !windows.length ? "No shorter windows are qualified for this stage." : stage.kind === "recognition" ? "Shorter recognition windows may change language context, wording and accuracy. This is a separate opt-in." : "Only the qualified window lengths and overlap policy can be used."}</p>
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
