import type { AdaptiveExecutionPolicy } from "./adaptivePolicy.ts";
import type { AdaptivePlanSettings } from "./adaptiveLearning.ts";

export type RecoveryMode = "" | "fixed" | "stage_management" | "batch_management" | "cpu_fallback" | "shorter_windows";

export interface RecoveryParameters {
    recovery_mode?: RecoveryMode;
    reuse_checkpoints?: boolean | null;
    model_family?: string;
    device?: string;
    diarize?: boolean;
    diarize_model?: string;
    diarization_device?: string;
    adaptive_policy?: AdaptiveExecutionPolicy | null;
}

export function usesLegacyAuto(params: RecoveryParameters): boolean {
    if (params.recovery_mode) return false;
    if (params.device === "auto") return true;
    if (!params.diarize || params.diarize_model === "native") return false;
    const speakerDevice = params.diarization_device || (["nvidia_canary", "nvidia_canary_qwen", "nvidia_parakeet", "mistral_voxtral"].includes(params.model_family || "") ? "auto" : "same");
    return speakerDevice === "auto" || (speakerDevice === "same" && params.device === "auto");
}

export function recoveryModeLabel(params: RecoveryParameters): string {
    if (params.recovery_mode === "stage_management") return "Level 1 · Stage management";
    if (params.recovery_mode === "batch_management") return "Level 2 · Batch management";
    if (params.recovery_mode === "cpu_fallback") return "Level 3 · Explicit CPU fallback";
    if (params.recovery_mode === "shorter_windows") return "Level 4 · Opt-in shorter windows";
    if (params.recovery_mode === "fixed") return "Fixed settings";
    return usesLegacyAuto(params) ? "Legacy Auto fallback" : "Legacy fixed settings";
}

export function devicePolicyDescription(mode?: RecoveryMode): string {
    if (mode === "cpu_fallback" || mode === "shorter_windows") return "CPU and GPU select the initial device. Auto chooses an available initial device. A stage can move to CPU only when its device is unlocked and CPU fallback is explicitly enabled with a supported precision.";
    return mode ? "CPU and GPU are explicit device choices. Auto chooses an available device before this run starts; it does not retry a GPU failure on CPU in this recovery mode."
        : "CPU uses system RAM; GPU uses an NVIDIA GPU. Legacy Auto tries an available GPU and may retry eligible GPU failures on CPU with FP32.";
}

export function shouldReuseCheckpoints(value?: boolean | null): boolean {
    return value !== false;
}

export interface RunSubmissionOptions {
    reuse_checkpoints?: boolean;
}

export interface RecoveryAttempt {
    id: string;
    attempt_number: number;
    status: string;
    device?: string;
    precision?: string;
    batch_size?: number;
    window_seconds?: number;
    overlap_seconds?: number;
    stitching_version?: string;
    plan_version?: number;
    measurements?: {
        torch_peak_allocated_bytes?: number; torch_peak_reserved_bytes?: number;
        host_total_bytes?: number; host_available_before_bytes?: number; host_minimum_available_bytes?: number;
        gpu_total_bytes?: number; device_used_before_bytes?: number; device_peak_used_bytes?: number;
        process_peak_bytes?: number; available_after_bytes?: number; external_contention: boolean; ownership_unknown?: boolean;
        samples: number; elapsed_seconds: number; scope: string;
    } | null;
    reason?: string;
    error_message?: string;
    error_code?: string;
    started_at?: string;
    completed_at?: string;
}

export function recoveryMemoryRows(attempt: RecoveryAttempt): Array<{ label: string; bytes?: number }> {
    const m = attempt.measurements;
    if (!m) return [];
    const rows = [{ label: attempt.device === "cuda" ? "Owned process peak VRAM" : attempt.device === "cpu" ? "Owned process peak RSS" : "Owned process peak", bytes: m.process_peak_bytes }];
    if (attempt.device === "cpu" || m.host_total_bytes !== undefined) rows.push(
        { label: "Host or container memory capacity", bytes: m.host_total_bytes },
        { label: "Host available before", bytes: m.host_available_before_bytes },
        { label: "Minimum host memory available", bytes: m.host_minimum_available_bytes },
    );
    if (attempt.device === "cuda" || m.gpu_total_bytes !== undefined) rows.push(
        { label: "GPU capacity", bytes: m.gpu_total_bytes },
        { label: "Device used before", bytes: m.device_used_before_bytes },
        { label: "Device-wide peak", bytes: m.device_peak_used_bytes },
        { label: "Device memory available after", bytes: m.available_after_bytes },
        { label: "PyTorch peak allocated", bytes: m.torch_peak_allocated_bytes },
        { label: "PyTorch peak reserved", bytes: m.torch_peak_reserved_bytes },
    );
    return rows;
}

export interface RecoveryStage {
    id: string;
    kind: string;
    label?: string;
    status: string;
    recoverable_boundary?: boolean;
    checkpoint_id?: string;
    reused_from_execution_id?: string;
    attempts: RecoveryAttempt[];
}

export interface RecoveryTranscript {
    text: string;
    segments?: Array<{ start: number; end: number; text: string; speaker?: string }>;
}

export interface ExecutionRecovery {
    execution_id: string;
    available?: boolean;
    mode?: RecoveryMode;
    status: string;
    resumable: boolean;
    resume_unavailable_reason?: string;
    partial_transcript_available: boolean;
    partial_transcript?: RecoveryTranscript;
    stages: RecoveryStage[];
    learning?: { status: string; reason?: string; plans?: Array<{
        plan_id: string; scope_key: string; profile_revision: number; learning_generation: number; stage_key: string;
        settings: AdaptivePlanSettings;
    }> };
}

export function recoveryIsActive(status?: string): boolean {
    return ["pending", "running", "processing", "waiting"].includes(status || "");
}

export function canResumeExecution(recovery: ExecutionRecovery | undefined, executionID: string, otherRunActive = false): boolean {
    return !!recovery && recovery.execution_id === executionID && recovery.resumable === true
        && !recoveryIsActive(recovery.status) && !otherRunActive;
}

export function partialRecoveryText(recovery?: ExecutionRecovery): string | undefined {
    return recovery?.partial_transcript_available === true && typeof recovery.partial_transcript?.text === "string"
        ? recovery.partial_transcript.text : undefined;
}

export function partialRecoveryDownload(recovery: ExecutionRecovery | undefined, executionID: string): { filename: string; blob: Blob } | undefined {
    if (!recovery || recovery.execution_id !== executionID) return undefined;
    const text = partialRecoveryText(recovery);
    if (text === undefined) return undefined;
    const safeID = executionID.replace(/[^a-zA-Z0-9_-]/g, "_").slice(0, 80) || "execution";
    return { filename: `partial-transcript-${safeID}.txt`, blob: new Blob([text], { type: "text/plain;charset=utf-8" }) };
}

export function recoveryAttemptReasonLabel(reason: string): string {
    return ({
        initial: "Initial attempt",
        resume_same_settings: "Resumed with the saved settings",
        cleanup_retry: "One GPU retry after cleanup, with the same settings",
        legacy_cpu_fallback: "Legacy Auto retry on CPU with FP32",
        smaller_batch: "Retry with a smaller qualified batch, within your saved bound",
        batch_reduction: "Retry with a smaller qualified batch, within your saved bound",
        cpu_fallback: "Explicitly permitted CPU fallback using the saved CPU precision",
        shorter_window: "Retry with an explicitly permitted shorter audio window",
        shorter_windows: "Retry with an explicitly permitted shorter audio window",
        learned_start: "Starting from a compatible measured plan",
    } as Record<string, string>)[reason] || "Additional attempt recorded";
}

export function recoveryAttemptErrorLabel(code: string): string {
    return ({
        cuda_out_of_memory: "The GPU ran out of memory during this attempt.",
        host_out_of_memory: "System memory was exhausted during this attempt.",
        cuda_runtime_error: "A CUDA execution error stopped this attempt.",
        adapter_failed: "The model could not complete this attempt. Check its runtime, input and access.",
        cancelled: "This attempt was cancelled.",
        deadline_exceeded: "The saved execution deadline expired.",
        resource_wait_expired: "The wait for a GPU execution slot expired.",
        worker_interrupted: "The worker stopped before this stage completed.",
        checkpoint_serialization_failed: "The model output could not be converted into a valid checkpoint.",
        checkpoint_persistence_failed: "The model output could not be saved as a durable checkpoint.",
    } as Record<string, string>)[code] || "This attempt stopped with an unrecognized error code.";
}

export function stageLabel(stage: RecoveryStage): string {
    return stage.label || ({ prepare: "Prepare audio", recognition: "Recognition", recognize: "Recognition", alignment: "Timestamp alignment", align: "Timestamp alignment", diarization: "Speaker identification", diarize: "Speaker identification", assemble: "Assemble output", publish: "Publish output", combined: "Combined model output" }[stage.kind] ?? stage.kind.replaceAll("_", " "));
}
