import { useCallback, useEffect, useState } from "react";
import { useAuth } from "@/features/auth/hooks/useAuth";
import { Button } from "@/components/ui/button";
import { ExecutionPolicyControls } from "@/components/transcription/ExecutionPolicyControls";
import { SwitchField } from "@/components/transcription/FormHelpers";
import { DEFAULT_EXECUTION_POLICY, executionPolicyErrors, type ExecutionPolicy } from "@/features/transcription/hooks/executionPolicy";

export function ExecutionPolicySettings() {
    const { getAuthHeaders } = useAuth();
    const [saved, setSaved] = useState<ExecutionPolicy | null>(null);
    const [policy, setPolicy] = useState(DEFAULT_EXECUTION_POLICY);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");
    const [notice, setNotice] = useState("");
    const load = useCallback(async (signal?: AbortSignal) => {
        setLoading(true); setError("");
        try {
            const response = await fetch("/api/v1/user/settings", { headers: getAuthHeaders(), signal });
            if (!response.ok) throw new Error("Could not load execution defaults.");
            const data = await response.json();
            if (signal?.aborted) return;
            const current = { ...DEFAULT_EXECUTION_POLICY, ...data.execution_policy };
            setSaved(current); setPolicy(current);
        } catch {
            if (!signal?.aborted) setError("Could not load execution defaults. Try again.");
        } finally { if (!signal?.aborted) setLoading(false); }
    }, [getAuthHeaders]);
    useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort(); }, [load]);
    const save = async () => {
        setSaving(true); setError(""); setNotice("");
        try {
            const response = await fetch("/api/v1/user/settings", { method: "PUT", headers: { ...getAuthHeaders(), "Content-Type": "application/json" }, body: JSON.stringify({ execution_policy: policy }) });
            const data = await response.json();
            if (!response.ok) throw new Error(data.error ?? "Could not save execution defaults.");
            setSaved(data.execution_policy); setPolicy(data.execution_policy);
            setNotice("Saved. New runs use this policy unless they have an explicit override.");
        } catch (err) { setError(err instanceof Error ? err.message : "Could not save execution defaults."); }
        finally { setSaving(false); }
    };
    const dirty = JSON.stringify(saved) !== JSON.stringify(policy);
    return <section className="min-w-0 space-y-5 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-main)]/50 p-4 sm:p-6" aria-labelledby="execution-defaults-heading">
        <div><h3 id="execution-defaults-heading" className="text-lg font-medium">Automatic recovery</h3>
            <p className="mt-1 text-sm text-[var(--text-secondary)]">Global defaults across models. Profiles and individual runs can override these; queued runs keep their saved policy.</p></div>
        {loading ? <p role="status" className="text-sm text-[var(--text-secondary)]">Loading execution defaults…</p> : saved && <fieldset disabled={saving} className="min-w-0 space-y-5">
            <ExecutionPolicyControls policy={policy} onChange={(next) => { setPolicy(next); setNotice(""); }} />
            <SwitchField id="default-checkpoint-reuse" label="Reuse compatible completed stages" checked={policy.reuse_checkpoints} onCheckedChange={(reuse_checkpoints) => { setPolicy({ ...policy, reuse_checkpoints }); setNotice(""); }} description="Reuse requires the same recording and verified matching settings, runtime and model revisions. Turn this off to compute fresh outputs." />
            <p className="text-xs leading-5 text-[var(--text-secondary)]">Stage separation, model unloading and saving completed stages are always handled automatically where supported.</p>
            <div className="flex flex-wrap items-center gap-3"><Button onClick={save} disabled={!dirty || executionPolicyErrors(policy).length > 0}>{saving ? "Saving…" : "Save recovery settings"}</Button></div>
        </fieldset>}
        {error && <div role="alert" className="text-sm text-[var(--error-solid)]">{error}{!saved && <Button className="ml-2" variant="outline" size="sm" onClick={() => void load()}>Retry</Button>}</div>}
        {notice && <p role="status" className="text-sm text-[var(--success-solid)]">{notice}</p>}
    </section>;
}
