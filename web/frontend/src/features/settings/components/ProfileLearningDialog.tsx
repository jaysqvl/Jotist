import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { useAuth } from "@/features/auth/hooks/useAuth";
import { adaptiveSettingsSummary, buildLearningAction, learningPlanIsCurrent, measuredDuration, measuredMemory, type AdaptiveLearningResponse } from "@/features/transcription/hooks/adaptiveLearning";

export function ProfileLearningDialog({ profile, onClose, onChanged }: {
    profile: { id: string; name: string } | null; onClose: () => void; onChanged: () => void;
}) {
    const { getAuthHeaders } = useAuth();
    const [busy, setBusy] = useState(false);
    const [notice, setNotice] = useState("");
    const [actionError, setActionError] = useState("");
    const [scope, setScope] = useState("");
    const [restoreRevision, setRestoreRevision] = useState("");
    useEffect(() => { setScope(""); setNotice(""); setActionError(""); setRestoreRevision(""); }, [profile?.id]);
    const query = useQuery({
        queryKey: ["profileLearning", profile?.id], enabled: !!profile,
        queryFn: async (): Promise<AdaptiveLearningResponse> => {
            const response = await fetch(`/api/v1/profiles/${encodeURIComponent(profile!.id)}/adaptive-policy`, { headers: getAuthHeaders() });
            const body = await response.json();
            if (!response.ok) throw new Error(typeof body.error === "string" ? body.error : "Could not load profile learning.");
            return body;
        },
    });
    const data = query.data;
    const current = data?.profile_id === profile?.id ? data : undefined;
    const plans = (current?.plans || []).filter((plan) => !scope || plan.scope_key === scope);
    const observations = (current?.observations || []).filter((observation) => !scope || observation.scope_key === scope);
    const scopes = new Map([...(current?.plans || []), ...(current?.observations || [])].map((item) => [item.scope_key, item.scope]));
    const selectedRevision = current?.revisions?.find((revision) => String(revision.revision) === restoreRevision);
    const act = async (action: "reset" | "freeze" | "restore", target?: string | number) => {
        if (!current || busy) return;
        setBusy(true); setActionError(""); setNotice("");
        try {
            const request = buildLearningAction(current, action, target);
            const response = await fetch(request.path, { method: "POST", headers: { "Content-Type": "application/json", ...getAuthHeaders() }, body: JSON.stringify(request.body) });
            if (!response.ok) {
                const body = await response.json().catch(() => ({}));
                throw new Error(typeof body.error === "string" ? body.error : "The profile changed or this action is unavailable. Reload before retrying.");
            }
            await query.refetch();
            onChanged();
            setNotice(action === "reset" ? "Learned starts reset. Existing execution evidence remains available." : action === "freeze" ? "Measured plan frozen into a saved profile revision." : "Saved profile revision restored as a new revision.");
            setRestoreRevision("");
        } catch (error) { setActionError(error instanceof Error ? error.message : "Could not update profile learning."); }
        finally { setBusy(false); }
    };
    return <Dialog open={!!profile} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
        <DialogContent className="max-h-[90vh] w-[95vw] sm:max-w-4xl overflow-y-auto bg-[var(--bg-card)] text-[var(--text-primary)]">
            <DialogHeader><DialogTitle>Learning · {profile?.name}</DialogTitle><DialogDescription>Review measured starting plans separately from requested profile settings. Learning never changes model checkpoints or context.</DialogDescription></DialogHeader>
            {query.isLoading && <p role="status">Loading measured plans and revisions…</p>}
            {(query.error || actionError) && <div role="alert" className="space-y-2 text-sm text-[var(--warning-solid)]"><p>{actionError || (query.error instanceof Error ? query.error.message : "Could not load learning.")}</p><Button variant="outline" disabled={busy} onClick={() => void query.refetch()}>Reload learning</Button></div>}
            {notice && <p role="status" className="text-sm">{notice}</p>}
            {current && <div className="space-y-6">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <p className="text-sm">Profile revision {current.profile_revision} · learning generation {current.learning_generation} · {current.status.replaceAll("_", " ")}</p>
                    <Button variant="outline" size="sm" disabled={busy} onClick={() => void act("reset")}>Reset learned starts</Button>
                </div>
                <label className="block space-y-1 text-sm"><span>Stage and workload scope</span><select className="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--bg-main)] p-2" value={scope} onChange={(event) => setScope(event.target.value)}><option value="">All observed scopes</option>{[...scopes].map(([key, value]) => <option value={key} key={key}>{value.stage_key} · {value.workload_class} · {value.memory_domain} · {key.slice(0, 8)}</option>)}</select></label>
                <section className="space-y-3" aria-label="Learned starting plans">
                    <h3 className="font-semibold">Learned starting plans</h3>
                    {!plans.length && <p className="text-sm text-[var(--text-secondary)]">No qualified measured plan is available for this scope. A completed model download, cached output or an unmeasured attempt does not establish a plan to freeze.</p>}
                    {plans.map((plan) => <article key={plan.id} className="space-y-2 rounded-lg border border-[var(--border-subtle)] p-3">
                        <p className="text-sm font-medium">{plan.scope.stage_key} · {plan.scope.workload_class}{current.selected_plan_id === plan.id || current.selected_plan_ids?.includes(plan.id) ? " · selected start" : ""}</p>
                        <p className="text-xs text-[var(--text-secondary)]">{adaptiveSettingsSummary(plan.settings)}</p>
                        <p className="text-xs text-[var(--text-secondary)]">Source evidence: profile revision {plan.profile_revision}, learning generation {plan.learning_generation}. Current selection is shown separately from this retained provenance.</p>
                        <p className="text-xs">Measured peak {measuredMemory(plan.peak_memory_bytes)} · reserve {measuredMemory(plan.minimum_reserve_bytes)} · mean processing {measuredDuration(plan.mean_processing_milliseconds)} · {plan.observation_ids.length} supporting observation(s)</p>
                        <Button variant="outline" size="sm" disabled={busy || !learningPlanIsCurrent(current, plan)} onClick={() => void act("freeze", plan.id)}>Freeze this measured plan</Button>
                        <p className="text-xs text-[var(--text-secondary)]">Freezing saves these stage settings, switches the profile to Fixed, and turns off learning. Other stages keep their requested settings.</p>
                    </article>)}
                </section>
                <section className="space-y-3" aria-label="Measured attempts">
                    <h3 className="font-semibold">Attempt evidence</h3>
                    {!observations.length && <p className="text-sm text-[var(--text-secondary)]">No observations recorded for this scope.</p>}
                    {observations.map((observation) => <details key={observation.id} className="rounded-lg border border-[var(--border-subtle)] p-3 text-xs">
                        <summary className="cursor-pointer">{observation.scope.stage_key} · {observation.outcome} · {observation.qualified ? "qualified" : "not qualified"}{observation.cached ? " · cached output" : ""}{observation.external_contention ? " · external contention" : ""}</summary>
                        {observation.profile_revision !== undefined && <p className="mt-2 text-[var(--text-secondary)]">Profile revision {observation.profile_revision} · learning generation {observation.learning_generation ?? "not recorded"}{observation.profile_revision !== current.profile_revision || observation.learning_generation !== current.learning_generation ? " · historical evidence, not the current learning generation" : ""}</p>}
                        <div className="mt-2 space-y-1 text-[var(--text-secondary)]"><p>{adaptiveSettingsSummary(observation.settings)}</p><p>Peak {measuredMemory(observation.peak_memory_bytes)} · available before {measuredMemory(observation.available_before_bytes)} · reserve {measuredMemory(observation.reserve_bytes)}</p><p>Loading {measuredDuration(observation.loading_milliseconds)} · processing {measuredDuration(observation.processing_milliseconds)} · {observation.full_stage ? "full stage" : "partial stage"}</p><p>Execution {observation.execution_id.slice(0, 8)} · attempt {observation.attempt_id.slice(0, 8)}</p></div>
                    </details>)}
                </section>
                <section className="space-y-3" aria-label="Profile revision history">
                    <h3 className="font-semibold">Restore saved profile settings</h3>
                    <p className="text-xs text-[var(--text-secondary)]">Restoring creates a new profile revision. It does not rewrite running executions, prior observations or downloaded model files.</p>
                    <select aria-label="Saved profile revision" className="w-full rounded-md border border-[var(--border-subtle)] bg-[var(--bg-main)] p-2 text-sm" value={restoreRevision} onChange={(event) => setRestoreRevision(event.target.value)}><option value="">Choose a previous revision</option>{(current.revisions || []).filter((revision) => revision.revision !== current.profile_revision).map((revision) => <option key={revision.revision} value={revision.revision}>Revision {revision.revision} · {revision.name} · {new Date(revision.saved_at).toLocaleString()} · {revision.reason}</option>)}</select>
                    {selectedRevision && <div className="space-y-2 rounded-lg border border-[var(--border-subtle)] p-3 text-sm"><p>{selectedRevision.name}</p><p className="text-xs text-[var(--text-secondary)]">{["model_family", "model", "device", "compute_type", "nvidia_precision", "recovery_mode"].filter((key) => selectedRevision.parameters[key] != null).map((key) => `${key.replaceAll("_", " ")}: ${String(selectedRevision.parameters[key])}`).join(" · ")}</p><Button variant="outline" disabled={busy} onClick={() => void act("restore", selectedRevision.revision)}>Restore revision {selectedRevision.revision}</Button></div>}
                </section>
            </div>}
        </DialogContent>
    </Dialog>;
}
