import { Section, SelectField, SwitchField } from "./FormHelpers";
import { recoveryModeLabel, shouldReuseCheckpoints, type RecoveryMode, type RecoveryParameters } from "@/features/transcription/hooks/recoveryPolicy";
import { ADAPTIVE_STAGE_LABELS, adaptiveLevel, qualifiedBatches, qualifiedWindows, stagePolicy, updateStagePolicy, type AdaptiveExecutionPolicy, type AdaptiveStageChoice, type AdaptiveStageKind, type AdaptiveStagePolicy } from "@/features/transcription/hooks/adaptivePolicy";
import { adaptiveSettingsSummary } from "@/features/transcription/hooks/adaptiveLearning";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";

export function CheckpointReuseField({ value, onChange }: { value?: boolean | null; onChange: (reuse: boolean) => void }) {
    return <div className="space-y-2">
        <SwitchField id="reuse-transcription-checkpoints" label="Reuse exact-compatible completed work" checked={shouldReuseCheckpoints(value)} onCheckedChange={onChange}
            description="New-run reuse requires this recording and verified matching settings, runtime and every model revision. Unverified or legacy runs can still resume their own selected checkpoints. Turn off for a fresh computation; prior results remain available." />
        <p className="text-xs leading-5 text-[var(--text-secondary)]">{shouldReuseCheckpoints(value) ? "Reused stages identify their source run and are not new inference measurements." : "Fresh submission: compute new outputs even when compatible checkpoints exist. This does not delete earlier checkpoints."}</p>
    </div>;
}

export function RecoveryPolicyFields({ params, stages, validationErrors, onModeChange, onReuseChange, onPolicyChange }: {
    params: RecoveryParameters; stages: AdaptiveStageChoice[]; validationErrors: string[];
    onModeChange: (mode: RecoveryMode) => void; onReuseChange: (reuse: boolean) => void;
    onPolicyChange: (policy: AdaptiveExecutionPolicy) => void;
}) {
    const legacy = !params.recovery_mode;
    const level = adaptiveLevel(params.recovery_mode);
    return <Section title="Recovery and execution policy" description="Completed work can be saved at boundaries exposed by the selected model. A checkpoint is saved output, separate from the downloaded model cache.">
        <SelectField label="Automatic recovery" value={params.recovery_mode || "legacy"} onValueChange={(value) => onModeChange(value === "legacy" ? "" : value as RecoveryMode)} options={[
            { value: "legacy", label: recoveryModeLabel({ ...params, recovery_mode: "" }) },
            { value: "fixed", label: "Off · Fixed settings" },
            { value: "stage_management", label: "Level 1 · Stage management" },
            { value: "batch_management", label: "Level 2 · Batch management" },
            { value: "cpu_fallback", label: "Level 3 · Explicit CPU fallback" },
            { value: "shorter_windows", label: "Level 4 · Opt-in shorter windows" },
        ]} />
        <p className="text-sm leading-6 text-[var(--text-secondary)]">{legacy
            ? "This profile keeps its existing device behavior. Choose a new mode explicitly to replace legacy Auto fallback; opening or saving the profile alone does not convert it."
            : level >= 2
                ? "The selected level is a permission ceiling. Only adapter-qualified actions enabled below can run. Context, model checkpoints and unapproved settings stay fixed; each attempt records its effective settings."
                : params.recovery_mode === "stage_management"
                ? "May schedule stages sequentially, release models at validated boundaries and retry one eligible GPU failure with the same settings. Batch size, precision, device and audio windows stay locked."
                : "Use the submitted settings. Save supported checkpoints and allow exact-plan resume, without adaptive changes or GPU-to-CPU failure retries."}</p>
        {!legacy && params.device === "auto" && <p className="text-xs leading-5 text-[var(--text-secondary)]">Auto chooses the initial device and keeps the requested starting precision. A later move to CPU requires Level 3 or 4, an unlocked stage device, and explicit CPU permission below.</p>}
        {(Object.keys(ADAPTIVE_STAGE_LABELS) as AdaptiveStageKind[]).filter((kind) => params.adaptive_policy?.stages?.[kind]?.fixed).map((kind) => <div key={kind} className="space-y-2 rounded-lg border border-[var(--border-subtle)] p-3">
            <p className="text-sm font-medium">Frozen settings · {ADAPTIVE_STAGE_LABELS[kind]}</p>
            <p className="text-xs text-[var(--text-secondary)]">{adaptiveSettingsSummary(params.adaptive_policy?.stages?.[kind]?.fixed)}</p>
            <p className="text-xs text-[var(--text-secondary)]">These saved stage settings override the device, precision, batch and window controls above when this stage runs. Remove the override to use those controls.</p>
            <Button type="button" size="sm" variant="outline" onClick={() => onPolicyChange(updateStagePolicy(params.adaptive_policy, kind, { fixed: undefined }))}>Remove frozen stage override</Button>
        </div>)}
        {level >= 2 && <div className="space-y-4">
            {stages.map((stage) => <StagePermissions key={stage.kind} stage={stage} level={level} policy={stagePolicy(params.adaptive_policy, stage.kind)} onChange={(patch) => onPolicyChange(updateStagePolicy(params.adaptive_policy, stage.kind, patch))} />)}
        </div>}
        {level > 0 && <div className="space-y-2">
            <SwitchField id="adaptive-learn-starts" label="Learn compatible starting plans for this profile" checked={params.adaptive_policy?.learn === true} onCheckedChange={(learn) => onPolicyChange({ ...params.adaptive_policy, learn })} />
            <p className="text-xs leading-5 text-[var(--text-secondary)]">Only eligible measured attempts from the saved profile revision can inform later starts. Cache reuse, synthetic runs and incomplete measurements do not establish a successful starting plan. Manage measured plans, freeze and reset in the profile’s Learning view.</p>
        </div>}
        {!!validationErrors.length && <ul role="alert" className="list-disc space-y-1 pl-5 text-sm text-[var(--warning-solid)]">{validationErrors.map((error) => <li key={error}>{error}</li>)}</ul>}
        <CheckpointReuseField value={params.reuse_checkpoints} onChange={onReuseChange} />
        <details className="text-xs leading-5 text-[var(--text-secondary)]">
            <summary className="cursor-pointer font-medium">Availability and measurements</summary>
            <p className="mt-2">Unavailable controls mean this adapter has not declared a qualified policy for that stage. A combined backend may expose only one completed-output checkpoint; text inside an unfinished model call cannot be recovered.</p>
            <p className="mt-2">Requested settings, learned starts and actual attempt settings are separate. A successful run establishes completion, not transcription accuracy. Shorter recognition windows can change context and word accuracy, even with overlap.</p>
        </details>
    </Section>;
}

function PolicyCheckbox({ checked, disabled, label, onChange }: { checked: boolean; disabled?: boolean; label: string; onChange: (value: boolean) => void }) {
    return <label className={`flex items-start gap-2 text-sm ${disabled ? "text-[var(--text-tertiary)]" : "text-[var(--text-primary)]"}`}>
        <input type="checkbox" className="mt-1" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />{label}
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
                <p className="text-xs text-[var(--text-secondary)]">{level < 3 ? "Requires Level 3 or 4." : policy.device_locked ? "Unlock the device to allow an explicit CPU fallback." : !cpuPrecisions.length ? "This adapter has not declared supported CPU precisions." : "CPU fallback keeps the model and context, using the precision you select."}</p>
                <label className="block space-y-1 text-sm text-[var(--text-primary)]"><span>CPU fallback precision</span>
                    <select className="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--bg-main)] p-2" value={policy.cpu_precision} disabled={!cpuAllowed} onChange={(event) => onChange({ cpu_precision: event.target.value })}>
                        {!cpuPrecisions.includes(policy.cpu_precision) && <option value={policy.cpu_precision}>{policy.cpu_precision || "Choose explicitly"}{cpuPrecisions.length ? " (not supported)" : " · default, not enabled"}</option>}
                        {cpuPrecisions.map((precision) => <option key={precision} value={precision}>{precision}{precision === "float32" ? " · FP32" : ""}</option>)}
                    </select>
                </label>
            </div>
        </div>
        <PolicyCheckbox checked={policy.allow_shorter_windows} disabled={!windowsAllowed && !policy.allow_shorter_windows} label="Allow explicitly selected shorter windows" onChange={(value) => onChange({ allow_shorter_windows: value })} />
        <p className="text-xs text-[var(--text-secondary)]">{level < 4 ? "Requires Level 4." : !windows.length ? "No window candidates and stitching policy are qualified for this stage." : stage.kind === "recognition" ? "Shorter recognition windows may change language context, wording and accuracy. This is a separate opt-in." : "Only the declared window candidates and overlap policy can be used."}</p>
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
