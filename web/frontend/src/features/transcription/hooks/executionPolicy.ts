export interface ExecutionPolicy {
    automatic_recovery: boolean;
    recovery_strength?: RecoveryStrength;
    max_retries: number;
    backoff_seconds: number;
    max_backoff_seconds: number;
    reduce_batch_size: boolean;
    reuse_checkpoints: boolean;
}

export type RecoveryStrength = "standard" | "strong" | "aggressive";
export const RECOVERY_STRENGTHS: Array<{ value: RecoveryStrength; label: string; description: string }> = [
    { value: "standard", label: "Standard", description: "Retry the failed stage and use smaller supported batches. Keep the device and audio context." },
    { value: "strong", label: "Strong", description: "Also allow supported shorter audio windows and timing repairs. These can change the output." },
    { value: "aggressive", label: "Aggressive", description: "Try Strong recovery first, then a supported CPU fallback. This can take much longer." },
];
const DEFAULT_RETRIES: Record<RecoveryStrength, number> = { standard: 3, strong: 5, aggressive: 6 };

export function selectRecoveryStrength(policy: ExecutionPolicy, strength: RecoveryStrength): ExecutionPolicy {
    const previous = policy.recovery_strength ?? "standard";
    return { ...policy, recovery_strength: strength,
        max_retries: policy.max_retries === DEFAULT_RETRIES[previous] ? DEFAULT_RETRIES[strength] : policy.max_retries };
}

export const DEFAULT_EXECUTION_POLICY: ExecutionPolicy = {
    automatic_recovery: true, recovery_strength: "standard", max_retries: 3, backoff_seconds: 2,
    max_backoff_seconds: 30, reduce_batch_size: true, reuse_checkpoints: true,
};

export interface ExecutionPolicyParameters {
    execution_policy_source?: "global" | "override";
    execution_policy?: ExecutionPolicy;
    recovery_mode?: string;
    reuse_checkpoints?: boolean | null;
}

export function globalExecutionPolicy(policy?: Partial<ExecutionPolicy> | null): ExecutionPolicy {
    return { ...DEFAULT_EXECUTION_POLICY, ...policy };
}

// A new custom run starts from global recovery defaults, even when its model
// settings come from an older run. Never use this to hydrate a saved profile or
// resume an execution: those keep their explicit, saved policy.
export function newRunRecoveryParameters<T extends ExecutionPolicyParameters>(parameters: T): T {
    return { ...parameters, execution_policy_source: "global", execution_policy: undefined,
        recovery_mode: undefined, reuse_checkpoints: undefined };
}

export function executionPolicyErrors(policy: ExecutionPolicy): string[] {
    const errors: string[] = [];
    if (policy.recovery_strength && !RECOVERY_STRENGTHS.some(({ value }) => value === policy.recovery_strength)) errors.push("Choose Standard, Strong or Aggressive recovery.");
    if (!Number.isInteger(policy.max_retries) || policy.max_retries < 0 || policy.max_retries > 6) errors.push("Choose between 0 and 6 retries.");
    if (!Number.isInteger(policy.backoff_seconds) || policy.backoff_seconds < 0 || policy.backoff_seconds > 120) errors.push("Initial delay must be between 0 and 120 seconds.");
    if (!Number.isInteger(policy.max_backoff_seconds) || policy.max_backoff_seconds < policy.backoff_seconds || policy.max_backoff_seconds > 300) errors.push("Maximum delay must be at least the initial delay and at most 300 seconds.");
    return errors;
}

export function sharedRecoveryMode(policy: ExecutionPolicy): "fixed" | RecoveryStrength {
    return !policy.automatic_recovery ? "fixed" : policy.recovery_strength ?? "standard";
}

export function executionPolicySummary(policy: ExecutionPolicy, savedMode?: string) {
    if (!policy.automatic_recovery || policy.max_retries === 0) return "Automatic retries off";
    const strength = !policy.recovery_strength && savedMode ? "Saved retry policy" : RECOVERY_STRENGTHS.find(({ value }) => value === (policy.recovery_strength ?? "standard"))?.label ?? "Standard";
    return `${strength} · up to ${policy.max_retries} retries per stage · ${policy.backoff_seconds}s initial delay`;
}

export function previewCheckpointReuse(params: ExecutionPolicyParameters, shared?: ExecutionPolicy) {
    return params.reuse_checkpoints ?? (params.execution_policy_source !== "override" ? shared?.reuse_checkpoints : params.execution_policy?.reuse_checkpoints) ?? true;
}
