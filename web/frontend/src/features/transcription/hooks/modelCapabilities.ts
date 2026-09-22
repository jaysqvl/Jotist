export interface TranscriptionModelCapability {
    model_id: string;
    model_family: string;
    display_name: string;
    description: string;
    supported_languages?: string[];
    requires_gpu?: boolean;
    memory_requirement_mb?: number;
    features: Record<string, boolean>;
    metadata?: Record<string, string>;
}

// The public API uses compact names; accept capability-shaped fixtures and older
// consumers too, but keep the rest of the UI on one typed representation.
export function normalizeModelCapabilities(models: Record<string, Record<string, unknown>>): TranscriptionModelCapability[] {
    const normalized = Object.entries(models).map(([key, model]) => ({
        model_id: String(model.id ?? model.model_id ?? key),
        model_family: String(model.family ?? model.model_family ?? ""),
        display_name: String(model.name ?? model.display_name ?? key),
        description: String(model.description ?? ""),
        supported_languages: (model.languages ?? model.supported_languages ?? []) as string[],
        requires_gpu: model.requires_gpu === true,
        memory_requirement_mb: Number(model.memory_mb ?? model.memory_requirement_mb ?? 0),
        features: (model.features ?? {}) as Record<string, boolean>,
        metadata: (model.metadata ?? {}) as Record<string, string>,
    }));
    return normalized.filter((model, index) => normalized.findIndex((candidate) => candidate.model_id === model.model_id) === index);
}

export function modelVariants(model?: TranscriptionModelCapability): string[] {
    try {
        const variants: unknown = JSON.parse(model?.metadata?.model_variants ?? "[]");
        return Array.isArray(variants) ? variants.filter((variant): variant is string => typeof variant === "string") : [];
    } catch {
        return [];
    }
}

export const LEGACY_MODEL_FAMILIES = [
    { value: "whisper", label: "Whisper" },
    { value: "nvidia_parakeet", label: "NVIDIA Parakeet" },
    { value: "nvidia_canary", label: "NVIDIA Canary" },
    { value: "nvidia_canary_qwen", label: "NVIDIA Canary-Qwen" },
    { value: "mistral_voxtral", label: "Mistral Voxtral" },
    { value: "openai", label: "OpenAI" },
];

export function isTranscriptionModel(model: TranscriptionModelCapability): boolean {
    return !["pyannote", "nvidia_sortformer", "diarizen", "suplime"].includes(model.model_family)
        && model.metadata?.task !== "diarization"
        && model.features?.diarization_only !== true;
}

export function contextSupport(model?: TranscriptionModelCapability) {
    const supported = model?.features?.context === true;
    return {
        prose: supported && !["terms", "keywords", "none"].includes(model?.metadata?.context_mode ?? "") && model?.features?.context_prose !== false,
        terms: supported && model?.metadata?.context_mode !== "none",
    };
}

export function findModelCapability(models: TranscriptionModelCapability[], family: string, model: string) {
    return models.find((entry) => entry.model_id === model)
        ?? (LEGACY_MODEL_FAMILIES.some((entry) => entry.value === family)
            ? models.find((entry) => entry.model_family === family || (family === "openai" && entry.model_family === "openai_whisper"))
            : undefined);
}

export function modelDetailsApply(model: TranscriptionModelCapability, variant: string) {
    return {
        benchmark: !model.metadata?.benchmark_model || model.metadata.benchmark_model === variant,
        memory: !model.metadata?.memory_model || model.metadata.memory_model === variant,
    };
}

export function requestedDiarizationDevice(family?: string, device?: string) {
    if (device) return device;
    return ["nvidia_parakeet", "nvidia_canary", "nvidia_canary_qwen", "mistral_voxtral"].includes(family ?? "") ? "auto" : "same";
}

export function transcriptionPrecision(params: { model_family: string; compute_type: string; nvidia_precision?: string }) {
    return ["nvidia_canary", "nvidia_canary_qwen"].includes(params.model_family)
        ? params.nvidia_precision || "float16"
        : params.compute_type;
}

interface CatalogSelectionParams {
    model_family: string;
    model: string;
    device: string;
    compute_type: string;
    task: string;
    batch_size: number;
    language?: string;
    audio_chunk_duration?: number | null;
    max_new_tokens?: number;
    diarize: boolean;
    diarize_model: string;
    diarization_checkpoint?: string;
    nvidia_precision?: string;
    nvidia_chunk_duration?: number;
    nvidia_use_chunking?: boolean;
    nvidia_prompt?: string;
}

// Apply defaults only after an explicit catalog selection. Loading or opening a
// saved profile must retain its original parameters, including legacy aliases.
export function selectCatalogModel<T extends CatalogSelectionParams>(
    params: T,
    key: "model_family" | "model",
    value: string,
    models: TranscriptionModelCapability[],
    isMultiTrack = false,
): T | undefined {
    const family = key === "model_family" ? value : params.model_family;
    const capability = models.find((model) => model.model_family === family
        && (key === "model_family" || model.model_id === value)
        && (model.model_id.includes("/") || !LEGACY_MODEL_FAMILIES.some((entry) => entry.value === family)));
    if (!capability) return undefined;

    const next = {
        ...params,
        model_family: family,
        model: capability.model_id,
        audio_chunk_duration: null,
        max_new_tokens: undefined,
    };
    if (key === "model_family") {
        next.device = "cpu";
        next.compute_type = capability.metadata?.default_precision || "float32";
        next.task = "transcribe";
        next.batch_size = 1;
        next.language = capability.supported_languages?.includes("auto") ? undefined : "en";
    }
    if (capability.metadata?.fixed_precision) next.compute_type = capability.metadata.fixed_precision;
    else if (next.device === "cpu" || !["float32", "float16", "bfloat16"].includes(next.compute_type)) next.compute_type = "float32";

    const previousCapability = findModelCapability(models, params.model_family, params.model);
    if (capability.features.integrated_diarization && !previousCapability?.features.integrated_diarization) {
        next.diarize_model = "native";
        next.diarize = true;
        next.diarization_checkpoint = undefined;
    } else if (!capability.features.integrated_diarization && next.diarize_model === "native") {
        next.diarize_model = "pyannote";
        next.diarization_checkpoint = undefined;
    }
    // External diarization selections and checkpoints survive ASR-only changes.
    if (isMultiTrack) next.diarize = false;
    if (capability.supported_languages?.length && !capability.supported_languages.includes(next.language || "auto")) {
        next.language = capability.supported_languages.includes("auto") ? undefined
            : capability.supported_languages.includes("en") ? "en" : capability.supported_languages[0];
    }
    return next;
}

export function modelFamilyOptions(models: TranscriptionModelCapability[]) {
    const options = [...LEGACY_MODEL_FAMILIES];
    for (const model of models.filter(isTranscriptionModel)) {
        if (!options.some((option) => option.value === model.model_family) && model.model_family !== "openai_whisper") {
            options.push({
                value: model.model_family,
                label: model.metadata?.family_display_name || model.display_name,
            });
        }
    }
    return options;
}

export function transcriptionModelLabel(family?: string, model?: string) {
    const exact: Record<string, string> = {
        "CohereLabs/cohere-transcribe-03-2026": "Cohere Transcribe 2B · March 2026",
        "Qwen/Qwen3-ASR-1.7B-hf": "Qwen3 ASR 1.7B",
        "Qwen/Qwen3-ASR-0.6B-hf": "Qwen3 ASR 0.6B",
        "ibm-granite/granite-speech-4.1-2b": "Granite Speech 4.1 2B",
        "ibm-granite/granite-speech-4.1-2b-plus": "Granite Speech 4.1 2B Plus",
        "ibm-granite/granite-speech-5.0-470m-turboctc": "Granite Speech 5.0 470M TurboCTC",
        "ibm-granite/granite-speech-5.0-470m-turboctc-nc": "Granite Speech 5.0 470M TurboCTC · NC",
        "Edge0/ARK-ASR-3B": "ARK ASR 3B",
        "OpenMOSS-Team/MOSS-Transcribe-Diarize": "MOSS Transcribe Diarize 0.9B",
        "OpenMOSS-Team/MOSS-Transcribe-preview-2B": "MOSS Transcribe Preview 2B",
        "mistralai/Voxtral-Mini-3B-2507": "Voxtral Mini 3B · July 2025",
        "mistralai/Voxtral-Mini-4B-Realtime-2602": "Voxtral Mini 4B Realtime · February 2026",
        "microsoft/VibeVoice-ASR-BitNet": "VibeVoice ASR BitNet 1.5B",
        "vibevoice-bitnet": "VibeVoice ASR BitNet 1.5B",
        "canary-1b-v2": "NVIDIA Canary 1B v2",
        "nvidia/canary-1b-v2": "NVIDIA Canary 1B v2",
        "nvidia/canary-qwen-2.5b": "NVIDIA Canary-Qwen 2.5B",
        "parakeet-tdt-0.6b-v3": "NVIDIA Parakeet TDT 0.6B v3",
        "nvidia/parakeet-tdt-0.6b-v3": "NVIDIA Parakeet TDT 0.6B v3",
    };
    if (model && exact[model]) return exact[model];
    const legacy: Record<string, string> = {
        nvidia_canary: "NVIDIA Canary",
        nvidia_canary_qwen: "NVIDIA Canary-Qwen",
        nvidia_parakeet: "NVIDIA Parakeet",
        mistral_voxtral: "Mistral Voxtral-mini",
    };
    if (model?.includes("/")) return model.split("/").pop()!;
    if (family && legacy[family]) return legacy[family];
    if (family === "openai") return `OpenAI ${model || "Whisper"}`;
    if (family === "whisper") return `Whisper ${model || ""}`.trim();
    return model?.split("/").pop() || family || "Transcription";
}

export const WHISPER_CHECKPOINTS = [
    "tiny", "tiny.en", "base", "base.en", "small", "small.en",
    "medium", "medium.en", "large", "large-v1", "large-v2", "large-v3",
];

export type ModelSort = "recommended" | "meeting" | "conversational" | "memory" | "gpu_memory";
export interface TranscriptionModelChoice {
    value: string;
    family: string;
    model: string;
    label: string;
    capability?: TranscriptionModelCapability;
    location: "local" | "cloud" | "unknown";
    meetingWER?: number;
    conversationalWER?: number;
    cpuRAM?: string;
    cpuPrecision?: string;
    gpuVRAM?: string;
    gpuPrecision?: string;
    gpuSupported?: boolean;
    hasOtherResults?: boolean;
    otherWER?: AdditionalBenchmark;
    recommendationRank?: number;
    recommendationReason?: string;
}

export function isCloudASR(family: string) {
    return ["openai", "openai_whisper"].includes(family);
}

export function modelChoiceValue(family: string, model: string) {
    return JSON.stringify([family, model]);
}

function numericMetric(value?: string) {
    if (!value?.trim()) return undefined;
    const number = Number(value);
    return Number.isFinite(number) && number >= 0 ? number : undefined;
}

export function modelExecutionLocation(family: string, capability?: TranscriptionModelCapability): "local" | "cloud" | "unknown" {
    if (isCloudASR(family)) return "cloud";
    if (capability?.metadata?.execution_location === "cloud" || capability?.metadata?.execution_location === "local") return capability.metadata.execution_location;
    return ["whisper", "nvidia_parakeet", "nvidia_canary", "nvidia_canary_qwen", "mistral_voxtral"].includes(family) ? "local" : "unknown";
}

function makeModelChoice(family: string, checkpoint: string, label: string, capability?: TranscriptionModelCapability): TranscriptionModelChoice {
    const applies = capability ? modelDetailsApply(capability, checkpoint) : { benchmark: false, memory: false };
    const metadata = capability?.metadata ?? {};
    const location = modelExecutionLocation(family, capability);
    const recommendation = capability ? meetingRecommendation(capability, checkpoint) : undefined;
    const memory = capability ? modelMemoryEstimate(capability, checkpoint) : undefined;
    const evaluations = capability ? additionalBenchmarks(capability, checkpoint) : [];
    return {
        value: modelChoiceValue(family, checkpoint), family, model: checkpoint,
        label: `${location === "local" ? "Local" : location === "cloud" ? "Cloud" : "Location unknown"} · ${label}`, capability, location,
        recommendationRank: recommendation?.rank, recommendationReason: recommendation?.reason,
        hasOtherResults: evaluations.length > 0,
        otherWER: evaluations.find((result) => result.metric.toUpperCase() === "WER"),
        cpuPrecision: memory?.cpuPrecision.toLowerCase().includes("quantized") ? "quantized" : "FP32",
        meetingWER: applies.benchmark ? numericMetric(metadata.benchmark_ami_wer) : undefined,
        conversationalWER: applies.benchmark ? numericMetric(metadata.benchmark_conversational_wer) : undefined,
        cpuRAM: location !== "cloud" ? memory?.cpuRAM : undefined,
        gpuVRAM: location !== "cloud" && memory ? gpuMemoryEstimate(memory).value : undefined,
        gpuPrecision: memory ? gpuMemoryEstimate(memory).precision : undefined,
        gpuSupported: memory?.gpuSupported,
    };
}

// All exact checkpoints share one ranking. Family-wide metadata is restricted by
// benchmark_model/memory_model before sorting, just as it is in the details view.
export function transcriptionModelChoices(models: TranscriptionModelCapability[], current?: { model_family: string; model: string }, cloudModels = ["whisper-1"]): TranscriptionModelChoice[] {
    const whisper = models.find((model) => model.model_family === "whisper");
    const choices = WHISPER_CHECKPOINTS.map((checkpoint) => makeModelChoice("whisper", checkpoint, `OpenAI Whisper ${checkpoint}`, whisper));
    for (const capability of models.filter(isTranscriptionModel)) {
        if (capability.model_family === "whisper" || isCloudASR(capability.model_family)) continue;
        const choice = makeModelChoice(capability.model_family, capability.model_id, capability.display_name, capability);
        if (!choices.some((existing) => existing.value === choice.value)) choices.push(choice);
    }
    const cloud = models.find((model) => isCloudASR(model.model_family));
    for (const checkpoint of new Set(["whisper-1", ...cloudModels])) {
        choices.push(makeModelChoice("openai", checkpoint, `OpenAI API ${checkpoint}`, cloud));
    }
    // Keep saved aliases and previously selected cloud checkpoints readable and
    // editable without changing a profile merely because the dialog was opened.
    if (current && !choices.some((choice) => choice.value === modelChoiceValue(current.model_family, current.model))) {
        const capability = findModelCapability(models, current.model_family, current.model);
        const saved = makeModelChoice(current.model_family, current.model,
            `${transcriptionModelLabel(current.model_family, current.model)} · saved selection`, capability);
        const aliasIndex = current.model_family === "mistral_voxtral" && current.model === "voxtral"
            ? choices.findIndex((choice) => choice.model === "mistralai/Voxtral-Mini-3B-2507") : -1;
        if (aliasIndex >= 0) choices[aliasIndex] = saved;
        else choices.push(saved);
    }
    return choices;
}

export function sortModelChoices(choices: TranscriptionModelChoice[], sort: ModelSort = "recommended") {
    const score = (choice: TranscriptionModelChoice) => {
        if (choice.location === "cloud") return Infinity;
        if (sort === "recommended") return choice.recommendationRank ?? Infinity;
        if (sort === "memory" || sort === "gpu_memory") {
            const numbers = (sort === "memory" ? choice.cpuRAM : choice.gpuVRAM)?.match(/\d+(?:\.\d+)?/g)?.map(Number);
            return numbers?.length ? Math.max(...numbers) : Infinity;
        }
        return (sort === "meeting" ? choice.meetingWER : choice.conversationalWER) ?? Infinity;
    };
    return [...choices].sort((a, b) => Number(a.location === "cloud") - Number(b.location === "cloud") || score(a) - score(b) || a.label.localeCompare(b.label) || a.value.localeCompare(b.value));
}

export function modelChoiceMetrics(choice: TranscriptionModelChoice, sort: ModelSort = "recommended") {
    if (choice.location === "cloud") return "WER no comparable result · uploads audio to OpenAI · server RAM n/a";
    const wer = sort === "conversational" ? choice.conversationalWER : choice.meetingWER;
    const alternative = sort === "recommended" && wer === undefined ? choice.otherWER : undefined;
    const result = alternative ? `WER ${alternative.value.toFixed(2)}% · ${alternative.dataset} · different test`
        : `${sort === "conversational" ? "Conversation" : "Meeting"} WER ${wer === undefined ? `no comparable result${choice.hasOtherResults ? " (other results available)" : ""}` : `${wer.toFixed(2)}%`}`;
    return `${sort === "recommended" && choice.recommendationRank !== undefined ? `#${choice.recommendationRank} · ` : ""}${result} · CPU ${choice.cpuPrecision || "FP32"} RAM est. ${choice.cpuRAM ? `${choice.cpuRAM} GB` : "unknown"} · ${choice.gpuSupported === false ? "GPU unsupported" : `GPU ${choice.gpuPrecision || "FP16/BF16"} VRAM est. ${choice.gpuVRAM ? `${choice.gpuVRAM} GB` : "unknown"}`}`;
}

export function selectTranscriptionModel<T extends CatalogSelectionParams>(params: T, choice: TranscriptionModelChoice, models: TranscriptionModelCapability[], isMultiTrack = false): T {
    if (isCloudASR(choice.family)) return {
        ...params, model_family: choice.family, model: choice.model, task: "transcribe",
        diarize: false,
        diarize_model: params.diarize_model === "native" ? "pyannote" : params.diarize_model,
        diarization_checkpoint: params.diarize_model === "native" ? undefined : params.diarization_checkpoint,
    };
    if (params.model_family === choice.family && params.model === choice.model) return params;
    const changedFamily = params.model_family !== choice.family;
    const orderedModels = choice.capability ? [choice.capability, ...models.filter((model) => model !== choice.capability)] : models;
    const selected = selectCatalogModel(params, changedFamily ? "model_family" : "model", changedFamily ? choice.family : choice.model, orderedModels, isMultiTrack);
    let next: T = selected ?? { ...params, model_family: choice.family, model: choice.model };
    if (!selected && changedFamily) {
        next = { ...next, device: "cpu", compute_type: "float32", batch_size: choice.family === "whisper" ? 8 : 1, task: "transcribe", audio_chunk_duration: null, max_new_tokens: undefined };
        if (next.diarize_model === "native") next = { ...next, diarize_model: "pyannote", diarization_checkpoint: undefined };
        if (choice.family.startsWith("nvidia_")) {
            next = { ...next, language: "en", nvidia_precision: "float32", nvidia_chunk_duration: choice.family === "nvidia_parakeet" ? 300 : 40, nvidia_use_chunking: choice.family === "nvidia_canary_qwen" };
            if (choice.family === "nvidia_canary_qwen") next = { ...next, max_new_tokens: 256, nvidia_prompt: "Transcribe the following:" };
        }
    }
    // An explicit external speaker configuration also survives entering a model
    // that offers native speakers. Native is still available in its own control.
    if (params.diarize && params.diarize_model !== "native") {
        next = { ...next, diarize: !isMultiTrack, diarize_model: params.diarize_model, diarization_checkpoint: params.diarization_checkpoint };
    }
    if (isMultiTrack) next = { ...next, diarize: false };
    return next;
}

export function hfTokenSource(params: { hf_token_source?: "default" | "custom" | "none"; hf_token?: string; has_hf_token?: boolean }) {
    return params.hf_token_source || (params.hf_token || params.has_hf_token ? "custom" : "default");
}

export function needsCustomHFToken(params: { hf_token_source?: "default" | "custom" | "none"; hf_token?: string; has_hf_token?: boolean }, editingExistingProfile: boolean) {
    return hfTokenSource(params) === "custom" && !params.hf_token?.trim() && !(editingExistingProfile && params.has_hf_token);
}

export interface AdditionalBenchmark {
    model: string;
    metric: string;
    dataset: string;
    value: number;
    source: string;
    source_label?: string;
    notes?: string;
    provenance?: string;
}

export function effectiveCheckpoint(model: TranscriptionModelCapability, variant?: string) {
    if (!variant || variant === model.model_id) return model.metadata?.model_id || model.model_id;
    if (model.model_family === "mistral_voxtral" && variant === "voxtral") return "mistralai/Voxtral-Mini-3B-2507";
    return variant;
}

export function additionalBenchmarks(model: TranscriptionModelCapability, variant?: string): AdditionalBenchmark[] {
    try {
        const rows: unknown = JSON.parse(model.metadata?.additional_benchmarks || "[]");
        const checkpoint = effectiveCheckpoint(model, variant);
        if (!Array.isArray(rows)) return [];
        return rows.filter((row): row is AdditionalBenchmark => row && typeof row === "object"
            && row.model === checkpoint && typeof row.metric === "string" && typeof row.dataset === "string"
            && typeof row.value === "number" && Number.isFinite(row.value) && typeof row.source === "string");
    } catch { return []; }
}

export function diarizationBenchmarkApplies(model: TranscriptionModelCapability, checkpoint?: string) {
    const benchmarkModel = model.metadata?.benchmark_model || model.metadata?.model_id;
    return !benchmarkModel || benchmarkModel === effectiveCheckpoint(model, checkpoint);
}


export function meetingRecommendation(model: TranscriptionModelCapability, variant?: string): { rank: number; reason: string } | undefined {
    const metadata = model.metadata ?? {};
    const checkpoint = effectiveCheckpoint(model, variant);
    try {
        const rows: unknown = JSON.parse(metadata.meeting_recommendations || "[]");
        const matched = Array.isArray(rows) ? rows.find((row) => row && row.model === checkpoint) : undefined;
        if (matched && typeof matched.rank === "number" && Number.isFinite(matched.rank)) return { rank: matched.rank, reason: typeof matched.reason === "string" ? matched.reason : "" };
    } catch { /* Single-checkpoint metadata remains usable if optional rows are malformed. */ }
    if (model.model_family === "whisper") return undefined;
    const rank = numericMetric(metadata.meeting_recommendation_rank);
    return rank === undefined ? undefined : { rank, reason: metadata.meeting_recommendation_reason || "" };
}

export interface ModelMemoryEstimate {
    cpuRAM?: string;
    cpuPrecision: string;
    gpuFloat16RAM?: string;
    gpuFloat32RAM?: string;
    gpuRAM?: string;
    gpuPrecision?: string;
    gpuSupported?: boolean;
    notes?: string;
    gpuNotes?: string;
}

export function modelMemoryEstimate(model: TranscriptionModelCapability, variant?: string): ModelMemoryEstimate {
    const metadata = model.metadata ?? {};
    const checkpoint = effectiveCheckpoint(model, variant);
    const text = (value: unknown) => typeof value === "string" && value.trim() ? value : undefined;
    try {
        const rows: unknown = JSON.parse(metadata.variant_memory_estimates || "[]");
        const matched = Array.isArray(rows) ? rows.find((row) => row && typeof row === "object" && row.model === checkpoint) : undefined;
        if (matched) return {
            cpuRAM: text(matched.cpu_ram_gb) || text(matched.cpu_float32_ram_gb), cpuPrecision: text(matched.cpu_memory_precision) || "FP32",
            gpuFloat16RAM: text(matched.gpu_float16_vram_gb), gpuFloat32RAM: text(matched.gpu_float32_vram_gb),
            gpuRAM: text(matched.gpu_vram_gb), gpuPrecision: text(matched.gpu_memory_precision),
            gpuSupported: metadata.supported_devices ? metadata.supported_devices.split(",").includes("cuda") : undefined,
            notes: text(matched.notes), gpuNotes: text(matched.gpu_notes) || text(matched.notes),
        };
    } catch { /* Use scoped adapter estimates when optional variant data is unavailable. */ }
    if (!modelDetailsApply(model, variant || checkpoint).memory) return { cpuPrecision: "FP32" };
    return {
        cpuRAM: metadata.cpu_ram_gb || metadata.cpu_float32_ram_gb || (model.memory_requirement_mb ? (model.memory_requirement_mb * 1024 * 1024 / 1e9).toFixed(1) : undefined),
        cpuPrecision: metadata.cpu_memory_precision || "FP32",
        gpuFloat16RAM: metadata.gpu_float16_vram_gb, gpuFloat32RAM: metadata.gpu_float32_vram_gb, gpuRAM: metadata.gpu_vram_gb,
        gpuPrecision: metadata.gpu_memory_precision,
        gpuSupported: metadata.supported_devices ? metadata.supported_devices.split(",").includes("cuda") : undefined,
        notes: metadata.memory_estimate_notes, gpuNotes: metadata.gpu_memory_estimate_notes || metadata.memory_estimate_notes,
    };
}

// GPU default is prospective FP16/BF16 unless the runtime fixes its precision.
// Quantized and unknown modes must not borrow an FP16 estimate.
export function gpuMemoryEstimate(memory: ModelMemoryEstimate, precision?: string): { value?: string; precision: string } {
    const fixed = memory.gpuPrecision;
    if (fixed) return { value: memory.gpuRAM || (fixed.toLowerCase().startsWith("fp32") ? memory.gpuFloat32RAM : memory.gpuFloat16RAM), precision: fixed };
    if (precision === "float32") return { value: memory.gpuFloat32RAM || memory.gpuRAM, precision: "FP32" };
    if (!precision || ["float16", "bfloat16"].includes(precision)) return { value: memory.gpuFloat16RAM || memory.gpuRAM, precision: "FP16/BF16" };
    return { precision };
}

export function referenceGPUFit(range?: string, budgetGB = 12): string {
    const values = range?.match(/\d+(?:\.\d+)?/g)?.map(Number);
    if (!values?.length) return "No GPU fit estimate available.";
    if (Math.min(...values) > budgetGB) return `Above the ${budgetGB} GB reference GPU budget; consider CPU if enough system RAM is available.`;
    if (Math.max(...values) >= budgetGB) return `May exceed the ${budgetGB} GB reference GPU budget; allow room for alignment and speaker processing.`;
    return `ASR estimate is below the ${budgetGB} GB reference GPU budget; alignment, speakers and other GPU processes need additional headroom.`;
}

export function alignmentMemoryEstimate(model: TranscriptionModelCapability): (ModelMemoryEstimate & { devicePolicy?: string; label?: string }) | undefined {
    try {
        const row = JSON.parse(model.metadata?.alignment_memory_estimates || "null");
        if (!row || typeof row !== "object" || Array.isArray(row)) return undefined;
        const metadata = Object.fromEntries(Object.entries(row).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
        return { ...modelMemoryEstimate({ ...model, metadata, memory_requirement_mb: 0 }), devicePolicy: metadata.device_policy, label: metadata.model, notes: metadata.notes, gpuNotes: metadata.gpu_notes || metadata.notes };
    } catch { return undefined; }
}
