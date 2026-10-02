import { useId } from "react";
import { Input } from "@/components/ui/input";
import { FormField, SwitchField } from "./FormHelpers";
import { RECOVERY_STRENGTHS, selectRecoveryStrength, executionPolicyErrors, executionPolicySummary, type ExecutionPolicy } from "@/features/transcription/hooks/executionPolicy";

export function ExecutionPolicyControls({ policy, savedMode, onChange }: {
    policy: ExecutionPolicy; savedMode?: string; onChange: (policy: ExecutionPolicy) => void;
}) {
    const id = useId();
    const update = (patch: Partial<ExecutionPolicy>) => onChange({ ...policy, ...patch });
    const errors = executionPolicyErrors(policy);
    const selectedStrength = policy.recovery_strength ?? (savedMode ? undefined : "standard");
    return <div className="min-w-0 space-y-4">
        <SwitchField id={`${id}-automatic-recovery`} label="Automatic recovery" checked={policy.automatic_recovery} onCheckedChange={(automatic_recovery) => update({ automatic_recovery })} />
        <p className="text-sm text-[var(--text-secondary)]">{policy.automatic_recovery
            ? "Choose what Jotist may change to finish a run. Completed stages are kept."
            : "Stop after a failed attempt. Saved work remains available for manual resume."}</p>
        <fieldset disabled={!policy.automatic_recovery} className="min-w-0 space-y-2 disabled:opacity-50">
            <legend className="sr-only">Recovery strength</legend>
            {RECOVERY_STRENGTHS.map((choice) => <label key={choice.value} className={`flex min-h-16 cursor-pointer items-start gap-3 rounded-xl border p-3 ${choice.value === selectedStrength ? "border-[var(--brand-solid)] bg-[var(--brand-solid)]/5" : "border-[var(--border-subtle)]"}`}>
                <input type="radio" name={`${id}-strength`} className="mt-1 h-4 w-4 shrink-0 accent-[var(--brand-solid)]" value={choice.value} checked={choice.value === selectedStrength} onChange={() => onChange(selectRecoveryStrength(policy, choice.value))} />
                <span className="min-w-0"><span className="block text-sm font-medium">{choice.label}</span><span className="mt-1 block text-xs leading-5 text-[var(--text-secondary)]">{choice.description}</span></span>
            </label>)}
        </fieldset>
        <details className="rounded-xl border border-[var(--border-subtle)]">
            <summary className="cursor-pointer px-4 py-3 text-sm font-medium">Retry limits & delays <span className="mt-1 block text-xs font-normal text-[var(--text-secondary)]">{executionPolicySummary(policy, savedMode)}</span></summary>
            <fieldset disabled={!policy.automatic_recovery} className="min-w-0 space-y-4 px-4 pb-4 disabled:opacity-50">
                <div className="grid min-w-0 gap-4 sm:grid-cols-3">
                    <FormField label="Maximum retries" htmlFor={`${id}-retries`}><Input id={`${id}-retries`} type="number" min={0} max={6} step={1} value={policy.max_retries} onChange={(e) => update({ max_retries: Number(e.target.value) })} /></FormField>
                    <FormField label="Initial delay (s)" htmlFor={`${id}-delay`}><Input id={`${id}-delay`} type="number" min={0} max={120} step={1} value={policy.backoff_seconds} onChange={(e) => update({ backoff_seconds: Number(e.target.value) })} /></FormField>
                    <FormField label="Maximum delay (s)" htmlFor={`${id}-max-delay`}><Input id={`${id}-max-delay`} type="number" min={policy.backoff_seconds} max={300} step={1} value={policy.max_backoff_seconds} onChange={(e) => update({ max_backoff_seconds: Number(e.target.value) })} /></FormField>
                </div>
                <p className="text-xs leading-5 text-[var(--text-secondary)]">One retry budget per stage, including retries inside the model. The delay doubles up to the maximum; cancellation stops the wait. Only supported actions are tried, within any saved stage constraints.</p>
                <SwitchField id={`${id}-batch-recovery`} label="Allow smaller supported batches" checked={policy.reduce_batch_size} onCheckedChange={(reduce_batch_size) => update({ reduce_batch_size })} description="Keep the model, precision, device and audio context." />
            </fieldset>
        </details>
        {errors.length > 0 && <ul role="alert" className="space-y-1 text-sm text-[var(--warning-solid)]">{errors.map((error) => <li key={error}>{error}</li>)}</ul>}
    </div>;
}
