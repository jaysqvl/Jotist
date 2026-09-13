import { findModelCapability, isCloudASR, type TranscriptionModelCapability, type TranscriptionModelChoice } from "./modelCapabilities.ts";

// Kept structural so the pure selection rules do not import the React dialog.
// The generic return value retains credentials, context and all other settings.
export interface SelectionDefaultParams {
    model_family: string;
    model: string;
    device: string;
    compute_type: string;
    batch_size: number;
    task: string;
    language?: string;
    fp16?: boolean;
    no_align?: boolean;
    chunk_size?: number;
    audio_chunk_duration?: number | null;
    max_new_tokens?: number;
    attention_context_left?: number;
    attention_context_right?: number;
    nvidia_precision?: string;
    nvidia_chunk_duration?: number;
    nvidia_use_chunking?: boolean;
    nvidia_timestamps?: boolean;
    nvidia_target_language?: string;
    nvidia_prompt?: string;
    diarize: boolean;
    diarize_model: string;
    diarization_device?: string;
    diarization_checkpoint?: string;
}

type ModelSelection = Pick<TranscriptionModelChoice, "family" | "model" | "capability">;

function metadataNumber(capability: TranscriptionModelCapability | undefined, key: string, fallback: number): number {
    const raw = capability?.metadata?.[key];
    if (!raw?.trim()) return fallback;
    const value = Number(raw);
    return Number.isSafeInteger(value) && value >= 0 ? value : fallback;
}

function supportedDevice(requested: string, family: string, capability?: TranscriptionModelCapability): string {
    const advertised = capability?.metadata?.supported_devices?.split(",").map((value) => value.trim()).filter(Boolean);
    const supported = advertised?.length ? advertised
        : family === "vibevoice-bitnet" ? ["cpu"]
        : capability?.requires_gpu ? ["cuda"]
        : ["cpu", "cuda", "auto"];
    if (supported.includes(requested)) return requested;
    const preferred = capability?.metadata?.default_device || "cpu";
    return supported.includes(preferred) ? preferred : supported[0];
}

function resourceDefaults<T extends SelectionDefaultParams>(params: T, requested: string, capability?: TranscriptionModelCapability): T {
    const device = supportedDevice(requested, params.model_family, capability);
    // Auto starts with GPU precision. The backend converts CPU resolution and
    // a classified GPU-failure retry to FP32 before starting CPU inference.
    const floatingPrecision = device === "cuda" || device === "auto" ? "float16" : "float32";
    // Parakeet currently exposes no precision switch and loads FP32 weights.
    const fixedPrecision = capability?.metadata?.fixed_precision
        || (params.model_family === "vibevoice-bitnet" ? "i2_s+i8_s" : undefined)
        || (params.model_family === "nvidia_parakeet" ? "float32" : undefined);
    const precision = fixedPrecision || floatingPrecision;
    const nvidiaPrecision = ["float32", "float16", "bfloat16"].includes(precision) ? precision : floatingPrecision;
    const next = { ...params, device, compute_type: precision, nvidia_precision: nvidiaPrecision, fp16: precision === "float16", batch_size: 1 };
    if (["nvidia_canary", "nvidia_canary_qwen"].includes(params.model_family)) {
        // Forty seconds matches the adapters' recognition window defaults;
        // enabling Canary chunking bounds long-meeting GPU memory use.
        next.nvidia_chunk_duration = 40;
        next.nvidia_use_chunking = true;
        // Keep the chosen external model/checkpoint while leaving room on the
        // reference 12 GB GPU for ASR. Users can explicitly change this after.
        if (device === "cuda" || device === "auto") next.diarization_device = "cpu";
    }
    return next;
}

function sourceLanguage(params: SelectionDefaultParams, capability?: TranscriptionModelCapability): string | undefined {
    if (params.model_family === "whisper") return params.model.endsWith(".en") ? "en" : params.language;
    if (isCloudASR(params.model_family)) return params.language;
    const supported = capability?.supported_languages;
    if (supported?.includes("*")) return params.language || "en";
    if (!supported?.length) return params.language || "en";
    if (supported.includes(params.language || "auto")) return params.language;
    return supported.includes("en") ? "en" : supported.includes("auto") ? undefined : supported[0];
}

// Invoke only for an explicit picker change, never during profile hydration.
export function applyModelSelectionDefaults<T extends SelectionDefaultParams>(
    params: T, choice: ModelSelection, models: TranscriptionModelCapability[], isMultiTrack = false,
): T {
    if (params.model_family === choice.family && params.model === choice.model) return params;
    const capability = choice.capability ?? findModelCapability(models, choice.family, choice.model);
    const previousCapability = findModelCapability(models, params.model_family, params.model);
    const next = resourceDefaults({ ...params, model_family: choice.family, model: choice.model, task: "transcribe" }, params.device, capability);
    next.language = sourceLanguage(next, capability);
    next.audio_chunk_duration = null;
    next.max_new_tokens = undefined;
    next.no_align = false;

    if (choice.family === "whisper") {
        next.chunk_size = 30;
    } else if (choice.family.startsWith("nvidia_")) {
        next.nvidia_timestamps = true;
        if (choice.family === "nvidia_parakeet") {
            next.attention_context_left = 256;
            next.attention_context_right = 256;
            next.nvidia_chunk_duration = 300;
            next.nvidia_use_chunking = true;
        } else if (choice.family === "nvidia_canary_qwen") {
            next.max_new_tokens = 256;
            // Preserve a deliberate compatible prompt, but populate the model
            // default when no prompt has been supplied.
            next.nvidia_prompt = params.nvidia_prompt || "Transcribe the following:";
        } else if (choice.family === "nvidia_canary") {
            next.nvidia_target_language = next.language || "en";
        }
    } else if (!isCloudASR(choice.family)) {
        const defaultChunk = metadataNumber(capability, "default_chunk_duration", capability?.features.integrated_diarization ? 0 : 30);
        const maxChunk = metadataNumber(capability, "max_chunk_duration", capability?.features.integrated_diarization ? 5400 : 30);
        next.audio_chunk_duration = Math.min(metadataNumber(capability, "fixed_chunk_duration", defaultChunk), maxChunk);
        next.max_new_tokens = metadataNumber(capability, "default_max_new_tokens", choice.family === "vibevoice-bitnet" ? 16384 : 0);
        // Native MOSS timing keeps a full meeting out of the separate aligner;
        // text-only ASR receives alignment for playback and external speakers.
        next.no_align = capability?.metadata?.timestamp_source?.startsWith("native") === true;
    }

    const hasExternalSelection = params.diarize && !["native", "none", ""].includes(params.diarize_model);
    if (capability?.features.integrated_diarization && !previousCapability?.features.integrated_diarization && !hasExternalSelection) {
        next.diarize = true;
        next.diarize_model = "native";
        next.diarization_checkpoint = undefined;
    } else if (!capability?.features.integrated_diarization && next.diarize_model === "native") {
        next.diarize_model = "pyannote";
        next.diarization_checkpoint = "pyannote/speaker-diarization-community-1";
    }
    if (isMultiTrack || isCloudASR(choice.family)) next.diarize = false;
    return next;
}

// Device changes reset only execution defaults. Language, decoding, token and
// alignment choices stay intact; opening a saved profile never calls this.
export function applyDeviceSelectionDefaults<T extends SelectionDefaultParams>(
    params: T, device: string, models: TranscriptionModelCapability[],
): T {
    if (params.device === device) return params;
    return resourceDefaults(params, device, findModelCapability(models, params.model_family, params.model));
}
