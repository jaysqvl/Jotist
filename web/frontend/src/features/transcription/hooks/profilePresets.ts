import { REFERENCE_PROFILE_VALUES } from "./referenceProfilePresets.ts";

export interface PresetParameters {
    [key: string]: unknown;
    model_family: string;
    model: string;
    device: "cpu" | "cuda";
    diarization_device: "cpu" | "cuda" | "auto" | "same";
    compute_type: string;
    nvidia_precision: string;
    fp16: boolean;
    batch_size: number;
    diarize: boolean;
    diarize_model: string;
    hf_token_source: "default";
    transcription_context: null;
    transcription_context_terms: null;
    is_multi_track_enabled: boolean;
}

export interface TranscriptionPreset {
    id: string;
    name: string;
    group: "CPU" | "GPU" | "Hybrid";
    origin: "existing" | "recommended";
    description: string;
    notes: string;
    parameters: PresetParameters;
}

type PresetModel = "canary" | "canary_qwen" | "whisper" | "parakeet" | "cohere" | "qwen";
type PresetDiarizer = "pyannote" | "nvidia_sortformer" | "none";

const checkpoints: Record<PresetModel, { family: string; model: string; label: string }> = {
    canary: { family: "nvidia_canary", model: "canary", label: "Canary" },
    canary_qwen: { family: "nvidia_canary_qwen", model: "canary_qwen", label: "Canary-Qwen" },
    whisper: { family: "whisper", model: "large-v3", label: "Whisper large-v3" },
    parakeet: { family: "nvidia_parakeet", model: "parakeet", label: "Parakeet" },
    cohere: { family: "cohere_transcribe", model: "CohereLabs/cohere-transcribe-03-2026", label: "Cohere Transcribe" },
    qwen: { family: "qwen3_asr", model: "Qwen/Qwen3-ASR-1.7B-hf", label: "Qwen3 ASR 1.7B" },
};

function preset(id: string, model: PresetModel, device: "cpu" | "cuda", diarizer: PresetDiarizer, notes: string, speakerDevice = device): TranscriptionPreset {
    const selected = checkpoints[model];
    const group = device !== speakerDevice ? "Hybrid" : device === "cpu" ? "CPU" : "GPU";
    const speakers = diarizer === "none" ? "without speakers" : diarizer === "pyannote" ? "Pyannote" : "Sortformer";
    return {
        id,
        name: `${group} ${selected.label} ${diarizer === "none" ? speakers : `+ ${speakers}`}`,
        group,
        origin: "recommended",
        description: `${selected.label} on ${device === "cpu" ? "CPU" : "GPU"}${diarizer === "none" ? ", with speaker labels disabled." : `, with ${speakers} on ${speakerDevice === "cpu" ? "CPU" : "GPU"}.`}`,
        notes,
        parameters: {
            model_family: selected.family,
            recovery_mode: "fixed",
            reuse_checkpoints: true,
            model: selected.model,
            device,
            diarization_device: speakerDevice,
            compute_type: device === "cpu" ? "float32" : "float16",
            nvidia_precision: device === "cpu" ? "float32" : "float16",
            fp16: device === "cuda",
            batch_size: 1,
            task: "transcribe",
            language: "en",
            diarize: diarizer !== "none",
            diarize_model: diarizer === "none" ? "pyannote" : diarizer,
            ...(diarizer === "pyannote" ? { diarization_checkpoint: "pyannote/speaker-diarization-community-1" } : {}),
            ...(diarizer === "nvidia_sortformer" ? { max_speakers: 4 } : {}),
            no_align: false,
            nvidia_chunk_duration: model === "parakeet" ? 30 : 40,
            nvidia_timestamps: true,
            nvidia_use_chunking: model !== "whisper",
            audio_chunk_duration: model === "cohere" || model === "qwen" ? 30 : null,
            hf_token_source: "default",
            transcription_context: null,
            transcription_context_terms: null,
            is_multi_track_enabled: false,
        },
    };
}

export const RECOMMENDED_PRESETS: readonly TranscriptionPreset[] = [
    preset("cpu-cohere-pyannote", "cohere", "cpu", "pyannote", "A strong candidate in the published meeting benchmark. Cohere and Pyannote require model access; allow room for word alignment."),
    preset("cpu-qwen-pyannote", "qwen", "cpu", "pyannote", "A strong candidate in the published conversational benchmark, with context and vocabulary hints. Pyannote requires model access."),
    preset("hybrid-canary-pyannote", "canary", "cuda", "pyannote", "For 12 GB GPU setups: Canary uses GPU and Pyannote uses CPU, reducing GPU demand for speaker attribution. Fit depends on recording length and settings.", "cpu"),
];

const referencePresets: TranscriptionPreset[] = REFERENCE_PROFILE_VALUES.map(({ name, ...parameters }) => {
    // Before the explicit device field existed, NVIDIA families selected a
    // diarization device independently, while Whisper followed its ASR device.
    const speakerDevice = parameters.model_family === "whisper" ? "same" : "auto";
    const mismatch = name.startsWith("GPU-") && parameters.device === "cpu";
    return {
        id: `reference-${name.toLowerCase()}`,
        name,
        group: parameters.device === "cpu" ? "CPU" : "GPU",
        origin: "existing",
        description: `Saved settings: ASR ${parameters.device === "cpu" ? "CPU" : "GPU"}, batch ${parameters.batch_size}${parameters.diarize ? `; speakers ${speakerDevice === "same" ? "follow transcription" : "use legacy Auto"}.` : "; no speaker labels."}`,
        notes: `${mismatch ? "Name/device mismatch preserved: this profile is named GPU but actually uses CPU for transcription. " : ""}${parameters.diarize && speakerDevice === "auto" ? "Legacy Auto may select a GPU for speakers, including in CPU-named profiles. " : ""}Original batch sizes, precision and chunk settings are retained. Credentials and context inherit Settings.`,
        parameters: {
            ...parameters,
            diarization_device: speakerDevice,
            hf_token_source: "default",
            transcription_context: null,
            transcription_context_terms: null,
        },
    };
});

export const TRANSCRIPTION_PRESETS: readonly TranscriptionPreset[] = [...referencePresets, ...RECOMMENDED_PRESETS];

export function presetAlreadyAdded(preset: TranscriptionPreset, existingNames: string[]) {
    return existingNames.some((name) => name.toLocaleLowerCase() === preset.name.toLocaleLowerCase());
}

export function createPresetDraft(preset: TranscriptionPreset, existingNames: string[]) {
    const usedNames = new Set(existingNames.map((name) => name.toLocaleLowerCase()));
    let name = preset.name;
    for (let suffix = 2; usedNames.has(name.toLocaleLowerCase()); suffix++) name = `${preset.name} (${suffix})`;
    return { name, description: preset.description, parameters: { ...preset.parameters } };
}
