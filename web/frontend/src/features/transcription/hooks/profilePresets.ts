import { applyModelSelectionDefaults } from "./selectionDefaults.ts";
import type { RecoveryParameters } from "./recoveryPolicy.ts";

export interface PresetParameters extends RecoveryParameters {
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
    origin: "recommended" | "alternative";
    description: string;
    notes: string;
    parameters: PresetParameters;
}

type PresetModel = "canary" | "canary_qwen" | "whisper" | "parakeet" | "cohere" | "qwen" | "qwen_small" | "granite_compact";
type PresetDiarizer = "pyannote" | "nvidia_sortformer" | "diarizen" | "none";

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
    const speakers = diarizer === "none" ? "without speakers" : diarizer === "pyannote" ? "Pyannote" : diarizer === "diarizen" ? "DiariZen" : "Sortformer";
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
        ...(diarizer === "diarizen" ? { diarization_checkpoint: "BUT-FIT/diarizen-wavlm-large-s80-md-v2" } : {}),
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
        origin: group === "GPU" ? "recommended" : "alternative",
        description: `${selected.label} on ${device === "cpu" ? "CPU" : "GPU"}${diarizer === "none" ? ", with speaker labels disabled." : `, with ${speakers} on ${speakerDevice === "same" ? "the transcription device" : speakerDevice === "cpu" ? "CPU" : "GPU"}.`}`,
        notes,
        parameters: applyModelSelectionDefaults(parameters, { family: selected.family, model: selected.model }, []),
    };
}

export const RECOMMENDED_PRESETS: readonly TranscriptionPreset[] = [
    preset("gpu-qwen-pyannote", "qwen", "cuda", "pyannote", "Qwen3 ASR 1.7B with context and vocabulary hints, plus Pyannote Community-1 speakers. Pyannote requires model access.", "same"),
    preset("gpu-parakeet-pyannote", "parakeet", "cuda", "pyannote", "Parakeet TDT 0.6B v3 keeps its native FP32 precision. Pyannote Community-1 requires model access.", "same"),
    preset("gpu-parakeet-sortformer", "parakeet", "cuda", "nvidia_sortformer", "Parakeet with Sortformer v2.1 speaker labels. Both runtimes use FP32; Sortformer supports up to four speakers.", "same"),
    preset("gpu-cohere-pyannote", "cohere", "cuda", "pyannote", "Cohere Transcribe with automatic decoder limits and Pyannote Community-1 speakers. Both models require model access.", "same"),
    preset("gpu-whisper-pyannote", "whisper", "cuda", "pyannote", "Whisper large-v3 with word alignment and Pyannote Community-1 speakers. Pyannote requires model access.", "same"),
    preset("gpu-canary-pyannote", "canary", "cuda", "pyannote", "Canary 1B v2 with 40-second recognition chunks and Pyannote Community-1 speakers. Pyannote requires model access.", "same"),
    preset("gpu-qwen-diarizen", "qwen", "cuda", "diarizen", "Qwen3 ASR 1.7B with DiariZen Large-s80-v2 speaker labels. DiariZen uses FP32 and needs memory for speaker embeddings.", "same"),
];

export const ALTERNATIVE_PRESETS: readonly TranscriptionPreset[] = [
    preset("cpu-cohere-pyannote", "cohere", "cpu", "pyannote", "Cohere and Pyannote Community-1 on CPU. Both require model access; word alignment also uses host memory."),
    preset("cpu-qwen-pyannote", "qwen", "cpu", "pyannote", "Qwen3 ASR 1.7B and Pyannote Community-1 on CPU, with context and vocabulary hints. Pyannote requires model access."),
    preset("cpu-qwen-small-pyannote", "qwen_small", "cpu", "pyannote", "Qwen3 ASR 0.6B and Pyannote Community-1 on CPU. The smaller Qwen model supports context and vocabulary hints."),
    preset("cpu-granite-compact-pyannote", "granite_compact", "cpu", "pyannote", "Granite Speech 5.0 470M with word alignment and Pyannote Community-1 speakers on CPU. Pyannote requires model access."),
    preset("hybrid-parakeet-pyannote", "parakeet", "cuda", "pyannote", "Parakeet on GPU with Pyannote Community-1 on CPU. Parakeet keeps FP32 precision; speaker processing uses host memory.", "cpu"),
];

// Quick Add creates public starting points; captured user configurations are
// historical fixtures and must not masquerade as another user's saved profiles.
export const TRANSCRIPTION_PRESETS: readonly TranscriptionPreset[] = [...RECOMMENDED_PRESETS, ...ALTERNATIVE_PRESETS];

export function presetAlreadyAdded(preset: TranscriptionPreset, existingNames: string[]) {
    return existingNames.some((name) => name.toLocaleLowerCase() === preset.name.toLocaleLowerCase());
}

export function createPresetDraft(preset: TranscriptionPreset, existingNames: string[]) {
    const usedNames = new Set(existingNames.map((name) => name.toLocaleLowerCase()));
    let name = preset.name;
    for (let suffix = 2; usedNames.has(name.toLocaleLowerCase()); suffix++) name = `${preset.name} (${suffix})`;
    return { name, description: preset.description, parameters: { ...preset.parameters } };
}
