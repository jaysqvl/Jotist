import { alignmentMemoryEstimate, findModelCapability, gpuMemoryEstimate, transcriptionPrecision, type TranscriptionModelCapability } from "./modelCapabilities.ts";
import type { RecoveryMode } from "./recoveryPolicy.ts";
import type { AdaptivePlanSettings } from "./adaptiveLearning.ts";

export type AdaptiveStageKind = "recognition" | "alignment" | "diarization";
export interface AdaptiveStagePolicy {
    fixed?: AdaptivePlanSettings;
    device_locked: boolean;
    allow_cpu: boolean;
    cpu_precision: string;
    min_batch_size: number;
    allow_shorter_windows: boolean;
    window_candidates: number[];
    min_window_seconds: number;
    overlap_seconds: number;
}
export interface AdaptiveExecutionPolicy {
    stages?: Partial<Record<AdaptiveStageKind, AdaptiveStagePolicy>>;
    learn: boolean;
    profile_id?: string;
    profile_revision?: number;
    learning_generation?: number;
}
export interface AdaptiveStageDescriptor {
    kind: string;
    schema_version?: string;
    implementation_version?: string;
    recoverable: boolean;
    model_artifacts?: Record<string, string>;
    device_precisions: Record<string, string[]>;
    qualified_batches?: number[];
    batch_parameter?: string;
    window_parameter?: string;
    precision_parameter?: string;
    default_precision?: string;
    combined_stages?: string[];
    qualification_notes?: string[];
    cancellable?: boolean;
    measurement_support?: string[];
    window_policy?: { unit: string; candidates: number[]; minimum_overlap: number; stitching_version: string };
}
export interface AdaptiveSelection {
    model_family: string; model: string; no_align?: boolean; diarize?: boolean; diarize_model?: string; diarization_checkpoint?: string;
    nvidia_timestamps?: boolean;
    recovery_mode?: RecoveryMode; adaptive_policy?: AdaptiveExecutionPolicy | null;
}
export interface AdaptiveStageChoice { kind: AdaptiveStageKind; label: string; descriptor?: AdaptiveStageDescriptor }

export const ADAPTIVE_STAGE_LABELS: Record<AdaptiveStageKind, string> = { recognition: "Recognition", alignment: "Timestamp alignment", diarization: "Speaker diarization" };
export function adaptiveLevel(mode?: RecoveryMode): number {
    return ({ stage_management: 1, batch_management: 2, cpu_fallback: 3, shorter_windows: 4 } as Record<string, number>)[mode || ""] || 0;
}
export function canonicalStageKind(kind: string): AdaptiveStageKind | undefined {
    return ({ recognize: "recognition", recognition: "recognition", combined: "recognition", align: "alignment", alignment: "alignment", diarize: "diarization", diarization: "diarization" } as Record<string, AdaptiveStageKind>)[kind];
}

// Only the adapter's declared stage descriptors authorize adaptive controls.
// Generic model features or estimated memory never qualify a policy action.
export function declaredAdaptiveStages(capability?: TranscriptionModelCapability): AdaptiveStageDescriptor[] {
    try {
        const value: unknown = JSON.parse(capability?.metadata?.adaptive_stages || "[]");
        if (!Array.isArray(value)) return [];
        return value.filter((stage): stage is AdaptiveStageDescriptor => !!stage && typeof stage === "object"
            && typeof stage.kind === "string" && canonicalStageKind(stage.kind) !== undefined
            && typeof stage.recoverable === "boolean" && !!stage.device_precisions && typeof stage.device_precisions === "object"
            && Object.values(stage.device_precisions).every((precisions) => Array.isArray(precisions) && precisions.every((precision) => typeof precision === "string")));
    } catch { return []; }
}

export function adaptiveStageChoices(params: AdaptiveSelection, models: TranscriptionModelCapability[]): AdaptiveStageChoice[] {
    const asr = findModelCapability(models, params.model_family, params.model);
    const descriptors = declaredAdaptiveStages(asr);
    const choices: AdaptiveStageChoice[] = [{ kind: "recognition", label: ADAPTIVE_STAGE_LABELS.recognition, descriptor: descriptors.find((stage) => canonicalStageKind(stage.kind) === "recognition") }];
    const alignmentRequested = params.model_family.startsWith("nvidia_") ? params.nvidia_timestamps !== false : params.no_align !== true;
    if (alignmentRequested) choices.push({ kind: "alignment", label: ADAPTIVE_STAGE_LABELS.alignment, descriptor: descriptors.find((stage) => canonicalStageKind(stage.kind) === "alignment") });
    if (params.diarize && params.diarize_model !== "native") {
        const family = params.diarize_model?.startsWith("pyannote/") ? "pyannote" : params.diarize_model;
        const candidates = models.filter((model) => model.model_id === family || model.model_family === family || (family === "sortformer" && model.model_family === "nvidia_sortformer"));
        const diarizer = candidates.find((model) => model.model_id === params.diarization_checkpoint || model.metadata?.model_id === params.diarization_checkpoint) || candidates[0];
        choices.push({ kind: "diarization", label: ADAPTIVE_STAGE_LABELS.diarization, descriptor: declaredAdaptiveStages(diarizer).find((stage) => canonicalStageKind(stage.kind) === "diarization") });
    }
    return choices;
}

export function stagePolicy(policy: AdaptiveExecutionPolicy | null | undefined, kind: AdaptiveStageKind): AdaptiveStagePolicy {
    const saved = policy?.stages?.[kind];
    return { device_locked: true, allow_cpu: false, cpu_precision: "float32", min_batch_size: 1, allow_shorter_windows: false, min_window_seconds: 0, overlap_seconds: 0, ...saved, window_candidates: [...(saved?.window_candidates || [])] };
}

// Display the initial alignment settings separately from permitted recovery
// actions. Memory estimates never qualify a device or a fallback candidate.
export function alignmentMemoryForConfiguration(params: AdaptiveSelection & { device: string; compute_type: string; nvidia_precision?: string }, models: TranscriptionModelCapability[]) {
    const capability = findModelCapability(models, params.model_family, params.model);
    const memory = capability ? alignmentMemoryEstimate(capability) : undefined;
    if (!memory) return undefined;
    const alignment = adaptiveStageChoices(params, models).find((choice) => choice.kind === "alignment");
    const rule = stagePolicy(params.adaptive_policy, "alignment");
    const fixed = alignment?.descriptor ? rule.fixed : undefined;
    const device = fixed?.device || (memory.devicePolicy === "cpu" ? "cpu" : params.device);
    const gpu = gpuMemoryEstimate(memory, fixed?.precision || transcriptionPrecision(params));
    const cpuFallbackPrecision = adaptiveLevel(params.recovery_mode) >= 3 && !rule.device_locked && rule.allow_cpu
        && alignment?.descriptor?.device_precisions.cpu?.includes(rule.cpu_precision) ? rule.cpu_precision : undefined;
    return { memory, enabled: !!alignment, device, precision: device === "cpu" ? memory.cpuPrecision : gpu.precision, gpuRAM: gpu.value, cpuFallbackPrecision, fixed: !!fixed };
}

export function updateStagePolicy(policy: AdaptiveExecutionPolicy | null | undefined, kind: AdaptiveStageKind, patch: Partial<AdaptiveStagePolicy>): AdaptiveExecutionPolicy {
    const next = { ...stagePolicy(policy, kind), ...patch };
    // Locking the device withdraws CPU permission, rather than hiding a live
    // fallback grant beneath a disabled control.
    if (patch.device_locked === true) next.allow_cpu = false;
    return { ...policy, learn: policy?.learn === true, stages: { ...policy?.stages, [kind]: next } };
}

export function qualifiedBatches(descriptor?: AdaptiveStageDescriptor): number[] {
    return [...new Set((Array.isArray(descriptor?.qualified_batches) ? descriptor.qualified_batches : []).filter((value) => Number.isSafeInteger(value) && value > 0))].sort((a, b) => a - b);
}
export function qualifiedWindows(descriptor?: AdaptiveStageDescriptor): number[] {
    const window = descriptor?.window_policy;
    if (!window || !window.stitching_version || window.unit !== "seconds" || !Array.isArray(window.candidates) || !Number.isFinite(window.minimum_overlap) || window.minimum_overlap < 0) return [];
    return [...new Set(window.candidates.filter((value) => Number.isSafeInteger(value) && value > 2 * window.minimum_overlap))].sort((a, b) => b - a);
}

export function adaptivePolicyErrors(params: Pick<AdaptiveSelection, "recovery_mode" | "adaptive_policy">, choices: AdaptiveStageChoice[]): string[] {
    const level = adaptiveLevel(params.recovery_mode);
    if (level < 2) return [];
    const errors: string[] = [];
    for (const { kind, label, descriptor } of choices) {
        const policy = stagePolicy(params.adaptive_policy, kind);
        if (!Number.isSafeInteger(policy.min_batch_size) || policy.min_batch_size < 1) errors.push(`${label}: minimum batch size must be a positive integer.`);
        if (level >= 3 && policy.allow_cpu) {
            if (policy.device_locked) errors.push(`${label}: unlock the stage device before allowing CPU fallback.`);
            if (!descriptor?.device_precisions.cpu?.includes(policy.cpu_precision)) errors.push(`${label}: CPU fallback requires a precision explicitly supported by this adapter.`);
        }
        if (level >= 4 && policy.allow_shorter_windows) {
            const candidates = qualifiedWindows(descriptor);
            if (!policy.window_candidates.length || policy.window_candidates.length > 2 || new Set(policy.window_candidates).size !== policy.window_candidates.length || policy.window_candidates.some((value) => !candidates.includes(value))) errors.push(`${label}: choose one or two distinct adapter-qualified windows.`);
            if (!Number.isSafeInteger(policy.min_window_seconds) || policy.min_window_seconds < 1 || policy.window_candidates.some((value) => value < policy.min_window_seconds)) errors.push(`${label}: the minimum window must be positive and no larger than any selected window.`);
            const minimum = descriptor?.window_policy?.minimum_overlap ?? 0;
            if (!Number.isFinite(policy.overlap_seconds) || policy.overlap_seconds < minimum || policy.window_candidates.some((value) => 2 * policy.overlap_seconds >= value)) errors.push(`${label}: overlap must meet the adapter minimum and be less than half of every selected window.`);
        }
    }
    return errors;
}
