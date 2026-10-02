import { useId } from "react";
import { Input } from "@/components/ui/input";
import { FormField, SwitchField } from "./FormHelpers";
import { executionPolicyErrors, executionPolicySummary, type ExecutionPolicy } from "@/features/transcription/hooks/executionPolicy";

export function ExecutionPolicyControls({ policy, onChange, showBatchOption = true }: {
    policy: ExecutionPolicy; onChange: (policy: ExecutionPolicy) => void; showBatchOption?: boolean;
}) {
    const id = useId();
    const update = (patch: Partial<ExecutionPolicy>) => onChange({ ...policy, ...patch });
    const errors = executionPolicyErrors(policy);
    return <div className="min-w-0 space-y-4">
        <SwitchField id={`${id}-automatic-recovery`} label="Automatic recovery" checked={policy.automatic_recovery} onCheckedChange={(automatic_recovery) => update({ automatic_recovery })} />
        <p className="text-sm text-[var(--text-secondary)]">{policy.automatic_recovery
            ? "Retry eligible failures from the affected stage. Completed work is kept."
            : "Stop after a failed attempt. Saved work remains available for manual resume."}</p>
        <details className="rounded-xl border border-[var(--border-subtle)]">
            <summary className="cursor-pointer px-4 py-3 text-sm font-medium">Retry options <span className="mt-1 block text-xs font-normal text-[var(--text-secondary)]">{executionPolicySummary(policy)}</span></summary>
            <fieldset disabled={!policy.automatic_recovery} className="min-w-0 space-y-4 px-4 pb-4 disabled:opacity-50">
                <div className="grid min-w-0 gap-4 sm:grid-cols-3">
                    <FormField label="Maximum retries" htmlFor={`${id}-retries`}><Input id={`${id}-retries`} type="number" min={0} max={6} step={1} value={policy.max_retries} onChange={(e) => update({ max_retries: Number(e.target.value) })} /></FormField>
                    <FormField label="Initial delay (s)" htmlFor={`${id}-delay`}><Input id={`${id}-delay`} type="number" min={0} max={120} step={1} value={policy.backoff_seconds} onChange={(e) => update({ backoff_seconds: Number(e.target.value) })} /></FormField>
                    <FormField label="Maximum delay (s)" htmlFor={`${id}-max-delay`}><Input id={`${id}-max-delay`} type="number" min={policy.backoff_seconds} max={300} step={1} value={policy.max_backoff_seconds} onChange={(e) => update({ max_backoff_seconds: Number(e.target.value) })} /></FormField>
                </div>
                <p className="text-xs leading-5 text-[var(--text-secondary)]">The delay doubles between retries up to the maximum. A stage retries only when it has an eligible recovery action; cancellation stops the wait.</p>
                {showBatchOption && <SwitchField id={`${id}-batch-recovery`} label="Allow smaller batches when memory is tight" checked={policy.reduce_batch_size} onCheckedChange={(reduce_batch_size) => update({ reduce_batch_size })} description="Uses only batch sizes supported by the stage. Model, precision, device and audio windows stay the same." />}
            </fieldset>
        </details>
        {errors.length > 0 && <ul role="alert" className="space-y-1 text-sm text-[var(--warning-solid)]">{errors.map((error) => <li key={error}>{error}</li>)}</ul>}
    </div>;
}
