import { transcriptionPrecision, type TranscriptionModelCapability } from "./modelCapabilities.ts";
import { recoveryModeLabel, type RecoveryParameters } from "./recoveryPolicy.ts";

export interface ExecutionSettings extends RecoveryParameters {
    model?: string;
    compute_type?: string;
    nvidia_precision?: string;
    nvidia_timestamps?: boolean;
    no_align?: boolean;
}

export function precisionLabel(value?: string): string {
    return ({ float32: "FP32", float16: "FP16", bfloat16: "BF16", int8: "INT8", int8_float16: "INT8 / FP16", int8_float32: "INT8 / FP32" } as Record<string, string>)[value || ""] || value || "Model default";
}

export function requestedExecutionPrecision(params: ExecutionSettings, capability?: TranscriptionModelCapability): string {
    const fixed = capability?.metadata?.fixed_precision || (params.model_family === "nvidia_parakeet" ? "float32" : params.model_family === "vibevoice-bitnet" ? "i2_s+i8_s" : undefined);
    if (fixed) return `${precisionLabel(fixed)} (fixed runtime)`;
    return precisionLabel(transcriptionPrecision({ model_family: params.model_family || "", compute_type: params.compute_type || "", nvidia_precision: params.nvidia_precision }));
}

export function reportedExecutionPrecision(params: ExecutionSettings, metadata?: Record<string, string>): string {
    if (!metadata?.precision) return "Not recorded";
    const label = precisionLabel(metadata.precision);
    return params.model_family === "nvidia_parakeet" && metadata.precision === "float32" && metadata.resolved_device === "cuda"
        ? `${label} (TF32 math enabled)` : label;
}

export function requestedExecutionTiming(params: ExecutionSettings, capability?: TranscriptionModelCapability): string {
    if (params.model_family?.startsWith("nvidia_")) return params.nvidia_timestamps === false ? "Disabled" : "Enabled";
    if (params.model_family === "openai" || params.model_family === "openai_whisper") return "API-provided timing";
    const nativeSource = capability?.metadata?.timestamp_source;
    if (nativeSource?.startsWith("native") || params.model === "OpenMOSS-Team/MOSS-Transcribe-Diarize" || params.model === "ibm-granite/granite-speech-4.1-2b-plus") {
        return params.no_align === false ? "Native timestamps and word alignment" : "Native timestamps";
    }
    if (params.no_align === true) return params.model_family === "whisper" ? "Segment timing; word alignment disabled" : "Word alignment disabled";
    if (params.no_align === false) return "Word alignment enabled";
    return "Model default";
}

export function reportedExecutionTiming(metadata?: Record<string, string>): string {
    if (!metadata?.timestamp_source) return "Not recorded";
    const sources: Record<string, string> = {
        none: "No timestamps reported",
        native: "Native timestamps",
        native_segments: "Native segment timestamps",
        native_word_timestamps: "Native word timestamps",
        native_word_ends_previous_end_starts: "Native word ends; inferred starts",
        qwen3_forced_alignment: "Aligned word timestamps",
        forced_alignment: "Aligned word timestamps",
    };
    return metadata.timestamp_source.split(",").map((source) => sources[source.trim()] || `Reported source: ${source.trim()}`).join("; ");
}

// Requested settings remain separate from runtime evidence, including legacy
// Auto fallback and native timing that does not depend on word alignment.
export function executionEvidenceRows(params: ExecutionSettings, metadata?: Record<string, string>, capability?: TranscriptionModelCapability) {
    return [
        { label: "Recovery policy", value: recoveryModeLabel(params) },
        { label: "Requested precision", value: requestedExecutionPrecision(params, capability) },
        { label: "Reported precision", value: reportedExecutionPrecision(params, metadata) },
        { label: "Requested timing", value: requestedExecutionTiming(params, capability) },
        { label: "Reported timing", value: reportedExecutionTiming(metadata) },
    ];
}
