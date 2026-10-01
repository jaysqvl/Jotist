import { isTranscriptionModel, modelVariants, type TranscriptionModelCapability } from "./modelCapabilities.ts";
import { diarizationModelLabel } from "./executionPresentation.ts";

export interface DiarizationChoice {
    value: string;
    diarizer: string;
    checkpoint?: string;
    label: string;
    capability?: TranscriptionModelCapability;
    savedOnly?: boolean;
}

export function diarizationChoiceValue(diarizer: string, checkpoint?: string) {
    return JSON.stringify([diarizer, checkpoint || ""]);
}

export function diarizationChoices(models: TranscriptionModelCapability[], current: { diarize_model: string; diarization_checkpoint?: string }, integrated = false) {
    const choices: DiarizationChoice[] = integrated
        ? [{ value: diarizationChoiceValue("native"), diarizer: "native", label: "Built into this model" }] : [];
    const speakers = models.filter((model) => !isTranscriptionModel(model));
    for (const model of speakers) {
        const diarizer = model.model_id === "sortformer" ? "nvidia_sortformer" : model.model_id;
        // Pyannote's older catalog omits variants declared by its parameter schema.
        const variants = model.model_id === "pyannote"
            ? ["pyannote/speaker-diarization-community-1", "pyannote/speaker-diarization-3.1"]
            : modelVariants(model);
        const checkpoints = variants.length ? variants : [model.metadata?.model_id];
        for (const checkpoint of checkpoints) choices.push({
            value: diarizationChoiceValue(diarizer, checkpoint), diarizer, checkpoint, capability: model,
            label: model.model_id === "sortformer" ? "NVIDIA Sortformer v2.1 · 4 speakers"
                : checkpoint ? diarizationModelLabel(checkpoint) : model.display_name,
        });
    }
    // Legacy servers still support these controls even without catalog metadata.
    if (!speakers.some((model) => model.model_id === "pyannote")) choices.push({ value: diarizationChoiceValue("pyannote"), diarizer: "pyannote", label: "Pyannote" });
    if (!speakers.some((model) => model.model_id === "sortformer")) choices.push({ value: diarizationChoiceValue("nvidia_sortformer"), diarizer: "nvidia_sortformer", label: "NVIDIA Sortformer" });
    const currentFamily = !current.diarize_model || current.diarize_model.startsWith("pyannote/") ? "pyannote"
        : current.diarize_model === "sortformer" ? "nvidia_sortformer" : current.diarize_model;
    const capability = speakers.find((model) => model.model_id === currentFamily || model.model_family === currentFamily);
    const checkpoint = current.diarization_checkpoint || (current.diarize_model.startsWith("pyannote/") ? current.diarize_model : capability?.metadata?.model_id);
    const value = diarizationChoiceValue(currentFamily, checkpoint);
    if (!choices.some((choice) => choice.value === value)) choices.push({
        value, diarizer: currentFamily, checkpoint, capability, savedOnly: true,
        label: `${diarizationModelLabel(checkpoint || currentFamily)} · saved selection`,
    });
    return { choices, value };
}
