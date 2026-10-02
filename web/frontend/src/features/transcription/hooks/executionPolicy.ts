export interface ExecutionPolicy {
    automatic_recovery: boolean;
    max_retries: number;
    backoff_seconds: number;
    max_backoff_seconds: number;
    reduce_batch_size: boolean;
    reuse_checkpoints: boolean;
}

export const DEFAULT_EXECUTION_POLICY: ExecutionPolicy = {
    automatic_recovery: true, max_retries: 3, backoff_seconds: 2,
    max_backoff_seconds: 30, reduce_batch_size: true, reuse_checkpoints: true,
};

export interface ExecutionPolicyParameters {
    execution_policy_source?: "global" | "override";
    execution_policy?: ExecutionPolicy;
    recovery_mode?: string;
    reuse_checkpoints?: boolean | null;
}

export function executionPolicyErrors(policy: ExecutionPolicy): string[] {
    const errors: string[] = [];
    if (!Number.isInteger(policy.max_retries) || policy.max_retries < 0 || policy.max_retries > 6) errors.push("Choose between 0 and 6 retries.");
    if (!Number.isInteger(policy.backoff_seconds) || policy.backoff_seconds < 0 || policy.backoff_seconds > 120) errors.push("Initial delay must be between 0 and 120 seconds.");
    if (!Number.isInteger(policy.max_backoff_seconds) || policy.max_backoff_seconds < policy.backoff_seconds || policy.max_backoff_seconds > 300) errors.push("Maximum delay must be at least the initial delay and at most 300 seconds.");
    return errors;
}

export function sharedRecoveryMode(policy: ExecutionPolicy): "fixed" | "batch_management" | "stage_management" {
    return !policy.automatic_recovery ? "fixed" : policy.reduce_batch_size ? "batch_management" : "stage_management";
}

export function executionPolicySummary(policy: ExecutionPolicy) {
    if (!policy.automatic_recovery || policy.max_retries === 0) return "Automatic retries off";
    return `Up to ${policy.max_retries} retries per stage · ${policy.backoff_seconds}s initial delay`;
}

export function previewCheckpointReuse(params: ExecutionPolicyParameters, shared?: ExecutionPolicy) {
    return params.reuse_checkpoints ?? (params.execution_policy_source === "global" ? shared?.reuse_checkpoints : params.execution_policy?.reuse_checkpoints) ?? true;
}
