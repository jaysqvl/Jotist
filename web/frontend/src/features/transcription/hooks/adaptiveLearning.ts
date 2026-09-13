export interface AdaptivePlanSettings {
    device: string; precision: string; batch_size: number; concurrency: number;
    window_seconds: number; overlap_seconds: number; stitching_version?: string;
}
export interface AdaptiveLearningScope {
    stage_key: string; workload_class: string; memory_domain: string; memory_capacity_bytes?: number;
    fixed_settings_hash?: string; runtime_fingerprint?: string; model_fingerprint?: string; hardware_fingerprint?: string;
}
export interface AdaptiveLearnedPlan {
    id: string; profile_revision: number; learning_generation: number; scope_key: string;
    scope: AdaptiveLearningScope; settings: AdaptivePlanSettings; observation_ids: string[];
    peak_memory_bytes?: number; minimum_reserve_bytes?: number; mean_processing_milliseconds?: number; created_at: string;
}
export interface AdaptiveObservation {
    id: string; execution_id: string; recording_id: string; attempt_id: string;
    profile_revision?: number; learning_generation?: number;
    scope_key: string; scope: AdaptiveLearningScope; settings: AdaptivePlanSettings;
    full_stage: boolean; cached: boolean; qualified: boolean; external_contention: boolean; outcome: string;
    peak_memory_bytes?: number; available_before_bytes?: number; reserve_bytes?: number;
    loading_milliseconds?: number; processing_milliseconds?: number; created_at: string;
}
export interface AdaptiveProfileRevision {
    revision: number; saved_at: string; reason: string; source_revision?: number; source_plan_id?: string;
    name: string; parameters: Record<string, unknown>;
}
export interface AdaptiveLearningResponse {
    profile_id: string; profile_revision: number; learning_generation: number; status: string;
    selected_plan_id?: string; selected_plan_ids?: string[]; plans: AdaptiveLearnedPlan[]; observations: AdaptiveObservation[];
    revisions: AdaptiveProfileRevision[];
}

export function measuredMemory(bytes?: number | null): string {
    if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return "Not measured";
    return bytes >= 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GiB` : `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
}
export function measuredDuration(milliseconds?: number | null): string {
    if (milliseconds == null || !Number.isFinite(milliseconds) || milliseconds < 0) return "Not measured";
    return `${(milliseconds / 1000).toFixed(1)}s`;
}
export function adaptiveSettingsSummary(settings?: AdaptivePlanSettings): string {
    if (!settings) return "No effective settings recorded";
    return `${settings.device} · ${settings.precision} · batch ${settings.batch_size} · concurrency ${settings.concurrency} · ${settings.window_seconds > 0 ? `${settings.window_seconds}s window` : "original/full window"} · ${settings.overlap_seconds}s overlap`;
}
export function learningPlanIsCurrent(data: AdaptiveLearningResponse, plan: AdaptiveLearnedPlan): boolean {
    // Selection is current authority. Immutable evidence keeps its source
    // generation/revision when promotion or a metadata edit advances a profile.
    return Array.isArray(plan.observation_ids) && plan.observation_ids.length >= 3
        && (data.selected_plan_id === plan.id || data.selected_plan_ids?.includes(plan.id) === true);
}
export function buildLearningAction(data: AdaptiveLearningResponse, action: "reset" | "freeze" | "restore", target?: string | number): { path: string; body: Record<string, string | number> } {
    if (!Number.isSafeInteger(data.profile_revision) || data.profile_revision < 1 || !Number.isSafeInteger(data.learning_generation) || data.learning_generation < 1) throw new Error("Reload the profile before changing its learning state.");
    const prefix = `/api/v1/profiles/${encodeURIComponent(data.profile_id)}`;
    if (action === "reset") return { path: `${prefix}/reset-adaptive`, body: { expected_revision: data.profile_revision, expected_generation: data.learning_generation } };
    if (action === "freeze") {
        const plan = data.plans.find((candidate) => candidate.id === target);
        if (!plan || !learningPlanIsCurrent(data, plan)) throw new Error("Choose a measured plan from the current profile revision and learning generation.");
        return { path: `${prefix}/freeze-adaptive`, body: { expected_revision: data.profile_revision, expected_generation: data.learning_generation, plan_id: plan.id } };
    }
    if (typeof target !== "number" || !data.revisions.some((revision) => revision.revision === target) || target === data.profile_revision) throw new Error("Choose a previous saved profile revision.");
    return { path: `${prefix}/restore-revision`, body: { expected_revision: data.profile_revision, expected_generation: data.learning_generation, revision: target } };
}
