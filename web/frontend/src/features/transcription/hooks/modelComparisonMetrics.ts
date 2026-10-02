import { additionalBenchmarks, diarizationBenchmarkApplies, gpuMemoryEstimate, modelMemoryEstimate, type TranscriptionModelCapability, type TranscriptionModelChoice } from "./modelCapabilities.ts";

export interface ModelMetric {
    label: string;
    value: string;
}

const percent = (value?: number) => value === undefined ? "—" : `${value.toFixed(2)}%`;
const memoryRange = (value?: string) => value ? `${value} GB` : "—";

export function transcriptionMetrics(choice: TranscriptionModelChoice, precision?: string): ModelMetric[] {
    const memory = choice.capability ? modelMemoryEstimate(choice.capability, choice.model) : undefined;
    const gpu = memory ? gpuMemoryEstimate(memory, precision) : undefined;
    return [
        { label: "AMI-Cleaned WER", value: percent(choice.meetingWER) },
        { label: "Conversation WER", value: percent(choice.conversationalWER) },
        { label: "GPU VRAM est.", value: choice.location === "cloud" ? "Cloud" : choice.gpuSupported === false ? "CPU only" : memoryRange(memory ? gpu?.value : choice.gpuVRAM) },
        { label: "CPU RAM est.", value: choice.location === "cloud" ? "Cloud" : memoryRange(choice.cpuRAM) },
    ];
}

export function diarizationMetrics(model: TranscriptionModelCapability, checkpoint?: string): { metrics: ModelMetric[]; note: string } {
    const metadata = model.metadata ?? {};
    const evidenceCheckpoint = diarizationEvidenceCheckpoint(model, checkpoint);
    const supportedSortformer = model.model_id !== "sortformer" || !metadata.memory_model || evidenceCheckpoint === metadata.memory_model;
    const rawDER = supportedSortformer && diarizationBenchmarkApplies(model, evidenceCheckpoint) ? metadata.benchmark_der : undefined;
    const parsedDER = rawDER?.trim() ? Number(rawDER) : undefined;
    const primaryDER = parsedDER !== undefined && Number.isFinite(parsedDER) && parsedDER >= 0 ? parsedDER : undefined;
    const otherDER = primaryDER === undefined ? additionalBenchmarks(model, evidenceCheckpoint).find((result) => result.metric.toUpperCase() === "DER") : undefined;
    const memory = modelMemoryEstimate(model, evidenceCheckpoint);
    const gpu = gpuMemoryEstimate(memory);
    return {
        metrics: [
            { label: "Published DER", value: percent(primaryDER ?? otherDER?.value) },
            { label: "GPU VRAM est.", value: memory.gpuSupported === false ? "CPU only" : memoryRange(gpu.value) },
            { label: "CPU RAM est.", value: memoryRange(memory.cpuRAM) },
        ],
        note: primaryDER !== undefined ? `${metadata.benchmark_der_dataset || "Published evaluation"} · compare only matching protocols.`
            : otherDER ? `${otherDER.provenance === "community" ? "Community" : "Publisher"} result · ${otherDER.dataset} · separate evaluation protocol.`
            : "No published DER for this checkpoint.",
    };
}

// Sortformer exposes one fixed runtime but older capabilities omit model_id.
export function diarizationEvidenceCheckpoint(model: TranscriptionModelCapability, checkpoint?: string) {
    return checkpoint || model.metadata?.model_id || (model.model_id === "sortformer" ? model.metadata?.memory_model : undefined);
}
