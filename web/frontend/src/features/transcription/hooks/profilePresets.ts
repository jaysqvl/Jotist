import { REFERENCE_PROFILE_VALUES } from "./referenceProfilePresets.ts";
import { applyModelSelectionDefaults } from "./selectionDefaults.ts";

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

type PresetModel = "canary" | "canary_qwen" | "whisper" | "parakeet" | "cohere" | "qwen" | "qwen_small" | "granite_compact";
type PresetDiarizer = "pyannote" | "nvidia_sortformer" | "none";

const checkpoints: Record<PresetModel, { family: string; model: string; label: string }> = {
    canary: { family: "nvidia_canary", model: "canary", label: "Canary" },
    canary_qwen: { family: "nvidia_canary_qwen", model: "canary_qwen", label: "Canary-Qwen" },
    whisper: { family: "whisper", model: "large-v3", label: "Whisper large-v3" },
    parakeet: { family: "nvidia_parakeet", model: "parakeet", label: "Parakeet TDT 0.6B v3" },
    cohere: { family: "cohere_transcribe", model: "CohereLabs/cohere-transcribe-03-2026", label: "Cohere Transcribe" },
    qwen: { family: "qwen3_asr", model: "Qwen/Qwen3-ASR-1.7B-hf", label: "Qwen3 ASR 1.7B" },
    qwen_small: { family: "qwen3_asr", model: "Qwen/Qwen3-ASR-0.6B-hf", label: "Qwen3 ASR 0.6B" },
    granite_compact: { family: "ibm_granite_speech", model: "ibm-granite/granite-speech-5.0-470m-turboctc", label: "Granite Speech 5.0 470M" },
};

function preset(id: string, model: PresetModel, device: "cpu" | "cuda", diarizer: PresetDiarizer, notes: string, speakerDevice: "cpu" | "cuda" | "same" = device): TranscriptionPreset {
    const selected = checkpoints[model];
    const group = speakerDevice !== "same" && device !== speakerDevice ? "Hybrid" : device === "cpu" ? "CPU" : "GPU";
    const speakers = diarizer === "none" ? "without speakers" : diarizer === "pyannote" ? "Pyannote" : "Sortformer";
    const parameters = {
        model_family: selected.family,
        execution_policy_source: "global",
        // Trigger the shared explicit-selection defaults for this new draft.
        model: "",
        device,
        diarization_device: speakerDevice,
        compute_type: "float32",
        nvidia_precision: "float32",
        fp16: false,
        batch_size: 1,
        task: "transcribe",
        language: "en",
        diarize: diarizer !== "none",
        diarize_model: diarizer === "none" ? "pyannote" : diarizer,
        ...(diarizer === "pyannote" ? { diarization_checkpoint: "pyannote/speaker-diarization-community-1" } : {}),
        ...(diarizer === "nvidia_sortformer" ? { max_speakers: 4 } : {}),
        hf_token_source: "default",
        transcription_context: null,
        transcription_context_terms: null,
        is_multi_track_enabled: false,
    } satisfies PresetParameters;
    return {
        id,
        name: `${group} ${selected.label} ${diarizer === "none" ? speakers : `+ ${speakers}`}`,
        group,
        origin: "recommended",
        description: `${selected.label} on ${device === "cpu" ? "CPU" : "GPU"}${diarizer === "none" ? ", with speaker labels disabled." : `, with ${speakers} on ${speakerDevice === "same" ? "the transcription device" : speakerDevice === "cpu" ? "CPU" : "GPU"}.`}`,
        notes,
        parameters: applyModelSelectionDefaults(parameters, { family: selected.family, model: selected.model }, []),
    };
}

export const RECOMMENDED_PRESETS: readonly TranscriptionPreset[] = [
    preset("gpu-qwen-pyannote", "qwen", "cuda", "pyannote", "GPU transcription and Community-1 speakers, with context and vocabulary hints. Uses shared recovery defaults; speakers follow the selected transcription device.", "same"),
    preset("cpu-cohere-pyannote", "cohere", "cpu", "pyannote", "A strong candidate in the published meeting benchmark. Cohere and Pyannote require model access; allow room for word alignment."),
    preset("cpu-qwen-pyannote", "qwen", "cpu", "pyannote", "A strong candidate in the published conversational benchmark, with context and vocabulary hints. Pyannote requires model access."),
    preset("hybrid-parakeet-pyannote", "parakeet", "cuda", "pyannote", "English GPU efficiency candidate: Parakeet TDT 0.6B v3 on GPU, with Community-1 speakers on CPU. Parakeet keeps FP32 weights. Full-recording runtime and memory still require qualification.", "cpu"),
    preset("cpu-qwen-small-pyannote", "qwen_small", "cpu", "pyannote", "Smaller English CPU candidate with context and vocabulary support. Uses less model memory than Qwen 1.7B; full-pipeline runtime still requires qualification."),
    preset("cpu-granite-compact-pyannote", "granite_compact", "cpu", "pyannote", "Compact Apache-licensed English CPU candidate. Word alignment and Community-1 speaker processing also need host memory and runtime qualification."),
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
