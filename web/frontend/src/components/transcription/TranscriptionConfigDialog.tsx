import { useState, useEffect, memo, createContext, useContext } from "react";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Loader2, Check, XCircle } from "lucide-react";
import { useAuth } from "@/features/auth/hooks/useAuth";
import { TranscriptionContextFields } from "./TranscriptionContextFields";
import { RecoveryPolicyFields } from "./RecoveryPolicyFields";
import { devicePolicyDescription, type RecoveryMode } from "@/features/transcription/hooks/recoveryPolicy";
import { adaptivePolicyErrors, adaptiveStageChoices, alignmentMemoryForConfiguration, type AdaptiveExecutionPolicy } from "@/features/transcription/hooks/adaptivePolicy";
import {
    LEGACY_MODEL_FAMILIES, contextSupport, findModelCapability, modelDetailsApply, requestedDiarizationDevice, normalizeModelCapabilities, modelVariants, isTranscriptionModel, transcriptionPrecision, transcriptionModelChoices, sortModelChoices, modelChoiceMetrics, modelChoiceValue, isCloudASR, modelExecutionLocation, hfTokenSource, needsCustomHFToken, additionalBenchmarks, diarizationBenchmarkApplies, modelMemoryEstimate, gpuMemoryEstimate, referenceGPUFit, type ModelSort,
    type TranscriptionModelCapability,
} from "@/features/transcription/hooks/modelCapabilities";
import { applyModelSelectionDefaults, applyDeviceSelectionDefaults } from "@/features/transcription/hooks/selectionDefaults";
import {
    FormField, Section, InfoBanner, SelectField, SwitchField, SliderField, AdvancedAccordion,
    inputClassName,
} from "@/components/transcription/FormHelpers";

// ============================================================================
// Types & Constants
// ============================================================================

export interface WhisperXParams {
    model_family: string;
    recovery_mode?: RecoveryMode;
    reuse_checkpoints?: boolean | null;
    adaptive_policy?: AdaptiveExecutionPolicy | null;
    model: string;
    model_cache_only: boolean;
    model_dir?: string;
    device: string;
    diarization_device?: string;
    diarization_checkpoint?: string;
    transcription_context?: string | null;
    transcription_context_terms?: string | null;
    audio_chunk_duration?: number | null;
    device_index: number;
    batch_size: number;
    compute_type: string;
    threads: number;
    output_format: string;
    verbose: boolean;
    task: string;
    language?: string;
    align_model?: string;
    interpolate_method: string;
    no_align: boolean;
    return_char_alignments: boolean;
    vad_method: string;
    vad_onset: number;
    vad_offset: number;
    chunk_size: number;
    diarize: boolean;
    min_speakers?: number;
    max_speakers?: number;
    diarize_model: string;
    speaker_embeddings: boolean;
    temperature: number;
    best_of: number;
    beam_size: number;
    patience: number;
    length_penalty: number;
    suppress_tokens?: string;
    suppress_numerals: boolean;
    initial_prompt?: string;
    condition_on_previous_text: boolean;
    fp16: boolean;
    temperature_increment_on_fallback: number;
    compression_ratio_threshold: number;
    logprob_threshold: number;
    no_speech_threshold: number;
    max_line_width?: number;
    max_line_count?: number;
    highlight_words: boolean;
    segment_resolution: string;
    hf_token?: string;
    hf_token_source?: "default" | "custom" | "none";
    has_hf_token?: boolean;
    print_progress: boolean;
    attention_context_left: number;
    attention_context_right: number;
    nvidia_chunk_duration: number;
    nvidia_timestamps?: boolean;
    nvidia_target_language?: string;
    nvidia_precision: string;
    nvidia_prompt?: string;
    nvidia_use_chunking?: boolean;
    is_multi_track_enabled: boolean;
    api_key?: string;
    max_new_tokens?: number;
}

interface TranscriptionConfigDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onStartTranscription: (params: WhisperXParams & { profileName?: string; profileDescription?: string }) => void;
    loading?: boolean;
    isProfileMode?: boolean;
    isExistingProfile?: boolean;
    initialParams?: Partial<WhisperXParams>;
    initialName?: string;
    initialDescription?: string;
    isMultiTrack?: boolean;
    title?: string;
    actionLabel?: string;
    loadingLabel?: string;
}

const DEFAULT_PARAMS: WhisperXParams = {
    model_family: "whisper",
    recovery_mode: "fixed",
    reuse_checkpoints: true,
    model: "small",
    model_cache_only: false,
    device: "cpu",
    diarization_device: "same",
    transcription_context: null,
    transcription_context_terms: null,
    device_index: 0,
    batch_size: 8,
    compute_type: "float32",
    threads: 0,
    output_format: "all",
    verbose: true,
    task: "transcribe",
    interpolate_method: "nearest",
    no_align: false,
    return_char_alignments: false,
    vad_method: "pyannote",
    vad_onset: 0.5,
    vad_offset: 0.363,
    chunk_size: 30,
    diarize: false,
    diarize_model: "pyannote",
    speaker_embeddings: false,
    temperature: 0,
    best_of: 5,
    beam_size: 5,
    patience: 1.0,
    length_penalty: 1.0,
    suppress_numerals: false,
    condition_on_previous_text: false,
    fp16: true,
    temperature_increment_on_fallback: 0.2,
    compression_ratio_threshold: 2.4,
    logprob_threshold: -1.0,
    no_speech_threshold: 0.6,
    highlight_words: false,
    segment_resolution: "sentence",
    print_progress: false,
    attention_context_left: 256,
    attention_context_right: 256,
    nvidia_chunk_duration: 300,
    nvidia_timestamps: true,
    nvidia_target_language: "en",
    nvidia_precision: "float16",
    nvidia_prompt: "",
    nvidia_use_chunking: false,
    is_multi_track_enabled: false,
    api_key: "",
};

const LANGUAGES = [
    { value: "auto", label: "Auto-detect" },
    { value: "af", label: "Afrikaans" },
    { value: "ar", label: "Arabic" },
    { value: "hy", label: "Armenian" },
    { value: "az", label: "Azerbaijani" },
    { value: "be", label: "Belarusian" },
    { value: "bs", label: "Bosnian" },
    { value: "bg", label: "Bulgarian" },
    { value: "ca", label: "Catalan" },
    { value: "zh", label: "Chinese" },
    { value: "hr", label: "Croatian" },
    { value: "cs", label: "Czech" },
    { value: "da", label: "Danish" },
    { value: "nl", label: "Dutch" },
    { value: "en", label: "English" },
    { value: "et", label: "Estonian" },
    { value: "fi", label: "Finnish" },
    { value: "fr", label: "French" },
    { value: "gl", label: "Galician" },
    { value: "de", label: "German" },
    { value: "el", label: "Greek" },
    { value: "he", label: "Hebrew" },
    { value: "hi", label: "Hindi" },
    { value: "hu", label: "Hungarian" },
    { value: "is", label: "Icelandic" },
    { value: "id", label: "Indonesian" },
    { value: "it", label: "Italian" },
    { value: "ja", label: "Japanese" },
    { value: "kn", label: "Kannada" },
    { value: "kk", label: "Kazakh" },
    { value: "ko", label: "Korean" },
    { value: "lv", label: "Latvian" },
    { value: "lt", label: "Lithuanian" },
    { value: "mk", label: "Macedonian" },
    { value: "ms", label: "Malay" },
    { value: "mr", label: "Marathi" },
    { value: "mi", label: "Maori" },
    { value: "ne", label: "Nepali" },
    { value: "no", label: "Norwegian" },
    { value: "fa", label: "Persian" },
    { value: "pl", label: "Polish" },
    { value: "pt", label: "Portuguese" },
    { value: "ro", label: "Romanian" },
    { value: "ru", label: "Russian" },
    { value: "sr", label: "Serbian" },
    { value: "sk", label: "Slovak" },
    { value: "sl", label: "Slovenian" },
    { value: "es", label: "Spanish" },
    { value: "sw", label: "Swahili" },
    { value: "sv", label: "Swedish" },
    { value: "tl", label: "Tagalog" },
    { value: "ta", label: "Tamil" },
    { value: "th", label: "Thai" },
    { value: "tr", label: "Turkish" },
    { value: "uk", label: "Ukrainian" },
    { value: "ur", label: "Urdu" },
    { value: "vi", label: "Vietnamese" },
    { value: "cy", label: "Welsh" },
];

const CANARY_LANGUAGES = [
    { value: "en", label: "English" },
    { value: "de", label: "German" },
    { value: "es", label: "Spanish" },
    { value: "fr", label: "French" },
    { value: "hi", label: "Hindi" },
    { value: "it", label: "Italian" },
    { value: "ja", label: "Japanese" },
    { value: "ko", label: "Korean" },
    { value: "pl", label: "Polish" },
    { value: "pt", label: "Portuguese" },
    { value: "ru", label: "Russian" },
    { value: "zh", label: "Chinese" },
];

const CANARY_QWEN_LANGUAGES = [
    { value: "en", label: "English" },
];

const PARAM_DESCRIPTIONS = {
    model: "For Whisper, start with small or medium. Use large-v3 when quality matters more than speed and you have enough VRAM.",
    language: "Pick the known language when you can. Auto-detect is convenient but can misread short, noisy, or multilingual clips.",
    task: "Use transcribe for same-language output. Use translate only when you want the model to produce another supported language.",
    device: "Choose where transcription runs. CPU uses system RAM; GPU (CUDA) uses an NVIDIA GPU. Auto tries an available GPU and retries on CPU after a GPU execution failure.",
    compute_type: "For Whisper on NVIDIA GPUs, float16 is the usual speed/quality choice. Use int8 to save VRAM; use float32 mostly for CPU or troubleshooting.",
    batch_size: "Higher can be faster but costs VRAM. On a 12GB RTX 3060, use 1 for Canary/Canary-Qwen, 1-2 for Parakeet, and raise only after a clean run.",
    diarize: "Adds speaker labels but uses more time and memory. Leave off while debugging model/VRAM failures.",
    diarize_model: "Pyannote is usually stronger but needs a Hugging Face token. Sortformer is local/NVIDIA and best when you expect up to four speakers.",
    temperature: "Keep 0 for repeatable transcripts. Raise only if Whisper gets stuck or repeats text.",
    beam_size: "Higher can improve Whisper decoding but slows it down. 5 is a good default; 1 is faster and lighter.",
    vad_method: "Voice detection affects segmentation before transcription. Pyannote is usually better; Silero is lighter when available.",
    initial_prompt: "Optional spelling, names, acronyms, or domain context. Keep it short and factual.",
    hf_token: "Needed for gated Pyannote diarization models. It is only sent to the backend for model access.",
    vad_onset: "Lower values catch quieter speech but may include noise. Try 0.35-0.45 for distant audio; 0.5 is a balanced default.",
    vad_offset: "Lower values end speech segments sooner. Try 0.25-0.35 for rapid dialogue; higher values keep pauses attached.",
    nvidia_chunk_duration: "When chunking is enabled, smaller chunks reduce peak VRAM but can lose long-range context. Try 20-40s on 12GB for Canary failures; use longer chunks when memory allows.",
    nvidia_timestamps: "Timestamps increase work and memory. Disable them for the first troubleshooting run, then re-enable if the model is stable.",
    nvidia_precision: "float16 usually saves VRAM on NVIDIA GPUs. bfloat16 can work on newer GPUs; float32 uses much more VRAM and is mainly for CPU/debugging.",
    nvidia_use_chunking: "Native mode keeps full-file context but can OOM on long audio. Enable chunking for long files or 12GB GPUs when Canary fails.",
    nvidia_prompt: "Canary-Qwen prompt. Keep the audio locator implicit and use short instructions like names, style, or vocabulary.",
    max_new_tokens: "For Canary-Qwen, this caps generated text per chunk. 256 is safe; lower it for memory/debugging, raise it only if chunks are cut off.",
};

// ============================================================================
// Main Component
// ============================================================================

const ModelCapabilitiesContext = createContext<TranscriptionModelCapability[]>([]);

export const TranscriptionConfigDialog = memo(function TranscriptionConfigDialog({
    open,
    onOpenChange,
    onStartTranscription,
    loading = false,
    isProfileMode = false,
    isExistingProfile = false,
    initialParams,
    initialName = "",
    initialDescription = "",
    isMultiTrack = false,
    title,
    actionLabel,
    loadingLabel,
}: TranscriptionConfigDialogProps) {
    const [params, setParams] = useState<WhisperXParams>(DEFAULT_PARAMS);
    const [profileName, setProfileName] = useState("");
    const [profileDescription, setProfileDescription] = useState("");
    const [defaultsNotice, setDefaultsNotice] = useState("");
    const [nameEdited, setNameEdited] = useState(false);
    const [descriptionEdited, setDescriptionEdited] = useState(false);

    // OpenAI validation state
    const [isValidating, setIsValidating] = useState(false);
    const [validationStatus, setValidationStatus] = useState<'idle' | 'valid' | 'invalid'>('idle');
    const [validationMessage, setValidationMessage] = useState("");
    const [availableModels, setAvailableModels] = useState<string[]>(["whisper-1"]);

    const { getAuthHeaders } = useAuth();
    const [modelSort, setModelSort] = useState<ModelSort>("recommended");
    const [hasSavedHFToken, setHasSavedHFToken] = useState(false);
    const [modelCapabilities, setModelCapabilities] = useState<TranscriptionModelCapability[]>([]);
    const [catalogLoading, setCatalogLoading] = useState(false);
    const [catalogError, setCatalogError] = useState("");
    const [savedContext, setSavedContext] = useState({ context: "", terms: "" });
    const [contextDefaultsError, setContextDefaultsError] = useState("");
    const selectedCapability = findModelCapability(modelCapabilities, params.model_family, params.model);
    const selectedContextSupport = contextSupport(selectedCapability);
    const isDynamicFamily = !LEGACY_MODEL_FAMILIES.some((family) => family.value === params.model_family);
    const isDynamicModel = isDynamicFamily || selectedCapability?.model_id.includes("/") === true;
    const modelChoices = sortModelChoices(transcriptionModelChoices(modelCapabilities, params, availableModels), modelSort);
    const selectedChoice = modelChoices.find((choice) => choice.value === modelChoiceValue(params.model_family, params.model));
    const cloudSelected = isCloudASR(params.model_family);
    const requiresCustomHFToken = !cloudSelected && needsCustomHFToken(params, isExistingProfile);
    const adaptiveStages = adaptiveStageChoices(params, modelCapabilities);
    const adaptiveErrors = cloudSelected ? [] : adaptivePolicyErrors(params, adaptiveStages);


    useEffect(() => {
        if (!open) return;
        const controller = new AbortController();
        setCatalogLoading(true);
        setCatalogError("");
        setContextDefaultsError("");
        const load = async () => {
            const results = await Promise.allSettled([
                fetch("/api/v1/transcription/models", { headers: getAuthHeaders(), signal: controller.signal }).then(async (response) => {
                    if (!response.ok) throw new Error("Could not load available models.");
                    const data = await response.json();
                    return normalizeModelCapabilities(data.models ?? {});
                }),
                fetch("/api/v1/user/settings", { headers: getAuthHeaders(), signal: controller.signal }).then(async (response) => {
                    if (!response.ok) throw new Error("Could not preview saved defaults. Inherited values will still be resolved when queued.");
                    return response.json();
                }),
            ]);
            if (controller.signal.aborted) return;
            if (results[0].status === "fulfilled") setModelCapabilities(results[0].value);
            else setCatalogError("Could not load the model catalog. Close and reopen to retry; existing model settings are still available.");
            if (results[1].status === "fulfilled") {
                setSavedContext({ context: results[1].value.transcription_context ?? "", terms: results[1].value.transcription_context_terms ?? "" });
                setHasSavedHFToken(results[1].value.has_hf_token === true);
            }
            else setContextDefaultsError("Could not preview saved defaults. Inherited values will still be resolved when queued.");
            setCatalogLoading(false);
        };
        void load();
        return () => controller.abort();
    }, [open, getAuthHeaders]);

    // Reset when dialog opens
    useEffect(() => {
        if (open) {
            const baseParams = {
                ...DEFAULT_PARAMS, ...initialParams,
                recovery_mode: initialParams ? initialParams.recovery_mode ?? "" : "fixed",
                reuse_checkpoints: initialParams ? initialParams.reuse_checkpoints : true,
                diarization_device: initialParams ? initialParams.diarization_device ?? "" : "same",
            };
            setParams({
                ...baseParams,
                is_multi_track_enabled: isMultiTrack,
                diarize: isMultiTrack ? false : baseParams.diarize
            });
            setDefaultsNotice("");
            setNameEdited(false);
            setDescriptionEdited(false);
            setModelSort("recommended");
            setProfileName(initialName);
            setProfileDescription(initialDescription);
        }
    }, [open, initialParams, initialName, initialDescription, isMultiTrack]);

    const updateDraftIdentity = (next: WhisperXParams) => {
        if (!isProfileMode || isExistingProfile) return;
        const choice = modelChoices.find((entry) => entry.value === modelChoiceValue(next.model_family, next.model));
        const name = choice?.label.replace(/^(Local|Cloud|Location unknown) · /, "") || next.model;
        const device = isCloudASR(next.model_family) ? "Cloud" : next.device === "cuda" ? "GPU" : next.device === "cpu" ? "CPU" : "Auto";
        const speaker = next.diarize_model === "native" ? "native speakers" : modelCapabilities.find((model) => model.model_id === (next.diarize_model === "nvidia_sortformer" ? "sortformer" : next.diarize_model))?.display_name || next.diarize_model;
        if (!nameEdited) setProfileName(`${device} ${name}${next.diarize ? ` + ${speaker}` : ""}`);
        if (!descriptionEdited) setProfileDescription(`${name} on ${device}${next.diarize ? `, with ${speaker}` : ""}.`);
    };

    const updateParam = <K extends keyof WhisperXParams>(key: K, value: WhisperXParams[K]) => {
        if (key === "device" && typeof value === "string") {
            const next = applyDeviceSelectionDefaults(params, value, modelCapabilities);
            setParams(next);
            updateDraftIdentity(next);
            setDefaultsNotice("Device defaults applied: precision, batch size and chunk settings updated. You can customize them below.");
        } else {
            setParams((previous) => ({ ...previous, [key]: value }));
            if (["diarize", "diarize_model", "diarization_device"].includes(key)) updateDraftIdentity({ ...params, [key]: value });
        }
    };

    const validateAPIKey = async () => {
        setIsValidating(true);
        setValidationStatus('idle');
        try {
            const response = await fetch('/api/v1/config/openai/validate', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
                body: JSON.stringify({ api_key: params.api_key }),
            });
            const data = await response.json();
            if (response.ok && data.valid) {
                setValidationStatus('valid');
                setAvailableModels(data.models || ["whisper-1"]);
                setValidationMessage("API key validated");
            } else {
                setValidationStatus('invalid');
                setValidationMessage(data.error || "Invalid API key");
            }
        } catch {
            setValidationStatus('invalid');
            setValidationMessage("Validation failed");
        } finally {
            setIsValidating(false);
        }
    };

    const handleSubmit = () => {
        if (requiresCustomHFToken || adaptiveErrors.length) return;
        if (isProfileMode) {
            onStartTranscription({ ...params, profileName, profileDescription });
        } else {
            onStartTranscription(params);
        }
    };

    const dialogTitle = title || (isProfileMode
        ? (initialName ? `Edit "${initialName}"` : "New Transcription Profile")
        : "Transcription Settings"
    );

    return (
        <ModelCapabilitiesContext.Provider value={modelCapabilities}>
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent
                className="max-w-full sm:max-w-2xl w-[calc(100vw-1rem)] max-h-[90vh] overflow-hidden flex flex-col p-0 gap-0 bg-[var(--bg-card)] border border-[var(--border-subtle)] rounded-2xl"
                style={{ boxShadow: 'var(--shadow-float)' }}
            >
                {/* Header */}
                <DialogHeader className="px-6 pt-6 pb-4 border-b border-[var(--border-subtle)]">
                    <DialogTitle className="text-xl font-semibold text-[var(--text-primary)]">
                        {dialogTitle}
                    </DialogTitle>
                    <DialogDescription className="text-[var(--text-secondary)] text-sm mt-1">
                        {isProfileMode
                            ? "Configure and save your transcription settings."
                            : "Choose a model and configure transcription parameters."
                        }
                    </DialogDescription>
                </DialogHeader>

                {/* Scrollable Content */}
                <div className="flex-1 overflow-y-auto px-6 py-6 space-y-6">

                    {/* Profile Name/Description (if profile mode) */}
                    {isProfileMode && (
                        <div className="p-4 bg-[var(--bg-main)] rounded-xl border border-[var(--border-subtle)] space-y-4">
                            <FormField label="Profile Name" htmlFor="profileName">
                                <Input
                                    id="profileName"
                                    value={profileName}
                                    onChange={(e) => { setNameEdited(true); setProfileName(e.target.value); }}
                                    placeholder="My transcription profile"
                                    className={inputClassName}
                                    required
                                />
                            </FormField>
                            <FormField label="Description" htmlFor="profileDesc" optional>
                                <Textarea
                                    id="profileDesc"
                                    value={profileDescription}
                                    onChange={(e) => { setDescriptionEdited(true); setProfileDescription(e.target.value); }}
                                    placeholder="Describe this profile..."
                                    className={`${inputClassName} resize-none min-h-[80px]`}
                                    rows={2}
                                />
                            </FormField>
                        </div>
                    )}

                    <div className="space-y-3">
                        <SelectField label="Sort models by" value={modelSort} onValueChange={(value) => setModelSort(value as ModelSort)} options={[
                            { value: "recommended", label: "Recommended for meetings" },
                            { value: "meeting", label: "Meeting WER · lowest first" },
                            { value: "conversational", label: "Conversational WER · lowest first" },
                            { value: "memory", label: "Estimated CPU RAM · lowest first" },
                            { value: "gpu_memory", label: "Estimated GPU VRAM · lowest first" },
                        ]} />
                        <SelectField label="Transcription model" value={modelChoiceValue(params.model_family, params.model)}
                            onValueChange={(value) => {
                                const choice = modelChoices.find((entry) => entry.value === value);
                                if (choice) {
                                    const next = applyModelSelectionDefaults(params, choice, modelCapabilities, isMultiTrack);
                                    setParams(next);
                                    updateDraftIdentity(next);
                                    setDefaultsNotice("Model defaults applied: precision, batch size and chunk settings updated for your device. Review speaker processing below.");
                                }
                            }}
                            options={modelChoices.map((choice) => ({ value: choice.value, label: choice.label, description: modelChoiceMetrics(choice, modelSort), disabled: false }))} />
                        {selectedChoice?.recommendationReason && <p className="text-sm leading-6 text-[var(--text-primary)]">Recommended #{selectedChoice.recommendationRank} · {selectedChoice.recommendationReason}</p>}
                        {modelSort === "recommended" && <p className="text-xs leading-5 text-[var(--text-secondary)]">Recommendation based on published results and meeting features; not measured on your recordings.</p>}
                        <p className="text-xs leading-5 text-[var(--text-secondary)]">Local models run on your Jotist server. Cloud APIs upload audio. Published WER is not accuracy on your recordings. RAM and VRAM are planning estimates for batch size 1. Unknown results sort last.</p>
                        {catalogLoading && <p role="status" className="text-xs text-[var(--text-secondary)]">Loading model capabilities…</p>}
                        {catalogError && <p role="alert" className="text-xs text-[var(--warning-solid)]">{catalogError}</p>}
                    </div>
                    {defaultsNotice && <p role="status" className="text-xs leading-5 text-[var(--text-secondary)]">{defaultsNotice}</p>}
                    {cloudSelected ? <InfoBanner variant="warning" title="Cloud · audio uploaded to OpenAI">
                        This model sends your recording to the OpenAI API for transcription. It runs on OpenAI’s servers and requires an API key.
                    </InfoBanner> : <>
                        <p className="text-sm font-medium text-[var(--text-primary)]">{modelExecutionLocation(params.model_family, selectedCapability) === "local" ? "Local · runs on your Jotist server" : "Check model execution location"}</p>
                        {selectedCapability && <ModelComparisonDetails model={selectedCapability} variant={params.model} device={params.device} precision={transcriptionPrecision(params)} batchSize={params.batch_size} />}
                        {selectedCapability && <PipelineMemoryDetails params={params} models={modelCapabilities} />}
                    </>}

                    {!cloudSelected && <RecoveryPolicyFields params={params} stages={adaptiveStages} validationErrors={adaptiveErrors} onModeChange={(value) => updateParam("recovery_mode", value)} onReuseChange={(value) => updateParam("reuse_checkpoints", value)} onPolicyChange={(value) => updateParam("adaptive_policy", value)} />}

                    <Section title="Transcription context" description="Guide recognition with meeting background and exact vocabulary.">
                        {catalogLoading && !selectedCapability ? (
                            <p className="text-sm text-[var(--text-secondary)]">Checking model support…</p>
                        ) : selectedContextSupport.prose || selectedContextSupport.terms ? (
                            <>
                                {!selectedContextSupport.prose && <p className="text-sm text-[var(--text-secondary)]">This model supports vocabulary hints. It does not use the meeting context paragraph.</p>}
                                {contextDefaultsError && <p role="status" className="text-xs text-[var(--warning-solid)]">{contextDefaultsError}</p>}
                                <TranscriptionContextFields
                                    context={params.transcription_context} terms={params.transcription_context_terms}
                                    onContextChange={(value) => updateParam('transcription_context', value)}
                                    onTermsChange={(value) => updateParam('transcription_context_terms', value)}
                                    defaultContext={savedContext.context} defaultTerms={savedContext.terms}
                                    proseSupported={selectedContextSupport.prose} termsSupported={selectedContextSupport.terms}
                                    allowInheritance idPrefix="run-transcription"
                                />
                            </>
                        ) : (
                            <p className="text-sm text-[var(--text-secondary)]">{selectedCapability ? "This model does not use context or vocabulary hints. Your saved defaults remain available for models that support them." : "Context support could not be verified until the model catalog loads."}</p>
                        )}
                    </Section>

                    {/* Multi-track notice */}
                    {isMultiTrack && (
                        <InfoBanner variant="info" title="Multi-track Audio Detected">
                            Each audio track will be transcribed separately. Speaker diarization is disabled.
                        </InfoBanner>
                    )}

                    {/* Model-Specific Configuration */}
                    {params.model_family === "whisper" && (
                        <WhisperConfig params={params} updateParam={updateParam} isMultiTrack={isMultiTrack} />
                    )}
                    {params.model_family === "nvidia_parakeet" && (
                        <ParakeetConfig params={params} updateParam={updateParam} isMultiTrack={isMultiTrack} />
                    )}
                    {params.model_family === "nvidia_canary" && (
                        <CanaryConfig params={params} updateParam={updateParam} isMultiTrack={isMultiTrack} />
                    )}
                    {params.model_family === "nvidia_canary_qwen" && (
                        <CanaryQwenConfig params={params} updateParam={updateParam} isMultiTrack={isMultiTrack} />
                    )}
                    {cloudSelected && <OpenAIConfig params={params} updateParam={updateParam}
                        isValidating={isValidating} validationStatus={validationStatus} validationMessage={validationMessage}
                        onValidate={validateAPIKey} />}
                    {params.model_family === "mistral_voxtral" && !isDynamicModel && (
                        <VoxtralConfig params={params} updateParam={updateParam} />
                    )}
                    {!cloudSelected && isDynamicModel && selectedCapability && (
                        <LocalModelConfig params={params} updateParam={updateParam} isMultiTrack={isMultiTrack} capability={selectedCapability} />
                    )}
                    {!cloudSelected && <HFTokenOverride params={params} updateParam={updateParam} hasSavedToken={hasSavedHFToken} requiresToken={requiresCustomHFToken} />}

                </div>

                {/* Footer */}
                <DialogFooter className="px-6 py-4 border-t border-[var(--border-subtle)] gap-3 sm:gap-2">
                    <Button
                        variant="ghost"
                        onClick={() => onOpenChange(false)}
                        className="rounded-xl text-[var(--text-secondary)] hover:bg-[var(--bg-main)] cursor-pointer"
                    >
                        Cancel
                    </Button>
                    <Button
                        onClick={handleSubmit}
                        disabled={loading || requiresCustomHFToken || adaptiveErrors.length > 0 || (isProfileMode && !profileName.trim())}
                        className="rounded-xl text-white cursor-pointer bg-gradient-to-r from-[#6356E5] to-[#5143C6] hover:opacity-90 active:scale-[0.98] transition-all shadow-lg shadow-brand-500/20"
                    >
                        {loading ? (
                            <>
                                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                                {loadingLabel || "Starting..."}
                            </>
                        ) : (
                            actionLabel || (isProfileMode ? "Save Profile" : "Start Transcription")
                        )}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
        </ModelCapabilitiesContext.Provider>
    );
});

// ============================================================================
// Shared Diarization Section
// ============================================================================

function DiarizationSection({ id, params, updateParam, description, integrated = false }: {
    id: string;
    params: WhisperXParams;
    updateParam: <K extends keyof WhisperXParams>(key: K, value: WhisperXParams[K]) => void;
    description?: string;
    integrated?: boolean;
}) {
    const capabilities = useContext(ModelCapabilitiesContext);
    const device = requestedDiarizationDevice(params.model_family, params.diarization_device);
    const diarizationModels = capabilities.filter((model) => !isTranscriptionModel(model));
    const diarizationCapability = capabilities.find((model) => model.model_id === (params.diarize_model === "nvidia_sortformer" ? "sortformer" : params.diarize_model));
    const checkpoints = modelVariants(diarizationCapability);
    return (
        <Section title="Speaker Diarization" description={description}>
            <div className="space-y-4">
                <SwitchField id={id} label="Enable speaker identification" description={PARAM_DESCRIPTIONS.diarize} checked={params.diarize} onCheckedChange={(v) => updateParam('diarize', v)} />

                {params.diarize && (
                    <div className="p-4 bg-[var(--bg-main)] rounded-xl border border-[var(--border-subtle)] space-y-4">
                        <SelectField
                            label="Diarization Model"
                            description="Choose the model that assigns speech to speakers. Check published DER, speaker limits, and model license before comparing runs."
                            value={params.diarize_model}
                            onValueChange={(v) => {
                                updateParam('diarize_model', v);
                                updateParam('diarization_checkpoint', undefined);
                            }}
                            options={[
                                ...(integrated ? [{ value: "native", label: "Built into this model" }] : []),
                                { value: "pyannote", label: "Pyannote" },
                                { value: "nvidia_sortformer", label: "NVIDIA Sortformer" },
                                ...diarizationModels.filter((model) => !["pyannote", "sortformer"].includes(model.model_id)).map((model) => ({ value: model.model_id, label: model.display_name })),
                            ]}
                        />

                        {checkpoints.length > 1 && (
                            <SelectField
                                label="Diarization checkpoint"
                                value={params.diarization_checkpoint || diarizationCapability?.metadata?.model_id || checkpoints[0]}
                                onValueChange={(value) => updateParam('diarization_checkpoint', value)}
                                options={checkpoints.map((value) => ({ value, label: value }))}
                            />
                        )}

                        {params.diarize_model === "native" ? (
                            <p className="text-sm text-[var(--text-secondary)]">Speaker labels are generated with the transcript on the transcription device.</p>
                        ) : (
                            <SelectField
                                label="Diarization device"
                                description="Same as transcription follows the actual transcription device after any fallback. Auto tries an available GPU and retries on CPU after a GPU execution failure."
                                value={device}
                                onValueChange={(value) => updateParam('diarization_device', value)}
                                options={[{ value: "same", label: "Same as transcription" }, { value: "cpu", label: "CPU" }, { value: "cuda", label: "GPU (CUDA)" }, { value: "auto", label: params.recovery_mode ? "Auto · choose once" : "Auto · GPU, then CPU" }]}
                            />
                        )}

                        {params.diarize_model !== "native" && diarizationCapability && (
                            <DiarizationComparisonDetails model={diarizationCapability} checkpoint={params.diarization_checkpoint} device={device === "same" ? params.device : device} />
                        )}

                        {params.diarize_model !== "native" && <div className="grid grid-cols-2 gap-4">
                            <FormField label="Min Speakers" optional>
                                <Input
                                    type="number" min={1} max={20} placeholder="Auto"
                                    value={params.min_speakers || ""}
                                    onChange={(e) => updateParam('min_speakers', e.target.value ? parseInt(e.target.value) : undefined)}
                                    className={inputClassName}
                                />
                            </FormField>
                            <FormField label="Max Speakers" optional>
                                <Input
                                    type="number" min={1} max={20} placeholder="Auto"
                                    value={params.max_speakers || ""}
                                    onChange={(e) => updateParam('max_speakers', e.target.value ? parseInt(e.target.value) : undefined)}
                                    className={inputClassName}
                                />
                            </FormField>
                        </div>}

                        {params.diarize_model === "pyannote" && (
                            <>
                                <div className="pt-3 border-t border-[var(--border-subtle)]">
                                    <p className="text-xs text-[var(--text-tertiary)] mb-3">Voice Detection Tuning (for noisy/distant audio)</p>
                                    <div className="grid grid-cols-2 gap-4">
                                        <FormField label="VAD Onset" description={PARAM_DESCRIPTIONS.vad_onset}>
                                            <Input
                                                type="number" min={0.1} max={0.9} step={0.05}
                                                value={params.vad_onset}
                                                onChange={(e) => updateParam('vad_onset', parseFloat(e.target.value) || 0.5)}
                                                className={inputClassName}
                                            />
                                        </FormField>
                                        <FormField label="VAD Offset" description={PARAM_DESCRIPTIONS.vad_offset}>
                                            <Input
                                                type="number" min={0.1} max={0.9} step={0.05}
                                                value={params.vad_offset}
                                                onChange={(e) => updateParam('vad_offset', parseFloat(e.target.value) || 0.363)}
                                                className={inputClassName}
                                            />
                                        </FormField>
                                    </div>
                                </div>
                            </>
                        )}
                    </div>
                )}
            </div>
        </Section>
    );
}

// ============================================================================
// Model-Specific Configuration Components
// ============================================================================

interface ConfigProps {
    params: WhisperXParams;
    updateParam: <K extends keyof WhisperXParams>(key: K, value: WhisperXParams[K]) => void;
    isMultiTrack?: boolean;
}

function WhisperConfig({ params, updateParam, isMultiTrack }: ConfigProps) {
    return (
        <div className="space-y-6">
            <Section title="Model Settings">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <SelectField label="Language" description={PARAM_DESCRIPTIONS.language} value={params.language || "auto"} onValueChange={(v) => updateParam('language', v === "auto" ? undefined : v)} options={LANGUAGES} />
                    <SelectField label="Task" description={PARAM_DESCRIPTIONS.task} value={params.task} onValueChange={(v) => updateParam('task', v)} options={[{ value: "transcribe", label: "Transcribe" }, { value: "translate", label: "Translate to English" }]} />
                    <SelectField label="Device" description={devicePolicyDescription(params.recovery_mode)} value={params.device} onValueChange={(v) => updateParam('device', v)} options={[{ value: "cpu", label: "CPU" }, { value: "cuda", label: "GPU (CUDA)" }, { value: "auto", label: params.recovery_mode ? "Auto · choose once" : "Auto · GPU, then CPU" }]} />
                </div>
            </Section>

            {!isMultiTrack && (
                <DiarizationSection id="diarize" params={params} updateParam={updateParam} description="Identify and separate different speakers in the audio" />
            )}

            <AdvancedAccordion>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <SelectField label="Compute Type" description={PARAM_DESCRIPTIONS.compute_type} value={params.compute_type} onValueChange={(v) => updateParam('compute_type', v)} options={[{ value: "float32", label: "Float32" }, { value: "float16", label: "Float16" }, { value: "int8", label: "Int8" }]} />
                    <FormField label="Batch Size" description={PARAM_DESCRIPTIONS.batch_size}>
                        <Input type="number" min={1} max={64} value={params.batch_size} onChange={(e) => updateParam('batch_size', parseInt(e.target.value) || 8)} className={inputClassName} />
                    </FormField>
                    <FormField label="Beam Size" description={PARAM_DESCRIPTIONS.beam_size}>
                        <Input type="number" min={1} max={10} value={params.beam_size} onChange={(e) => updateParam('beam_size', parseInt(e.target.value) || 5)} className={inputClassName} />
                    </FormField>
                    <FormField label="Temperature" description={PARAM_DESCRIPTIONS.temperature}>
                        <Input type="number" min={0} max={1} step={0.1} value={params.temperature} onChange={(e) => updateParam('temperature', parseFloat(e.target.value) || 0)} className={inputClassName} />
                    </FormField>
                </div>

                <FormField label="Additional Whisper prompt" description="Optional Whisper-specific prompt, kept for compatibility with existing profiles. Meeting context and vocabulary can be configured above." optional>
                    <Textarea
                        placeholder="Optional context to guide transcription..."
                        value={params.initial_prompt || ""}
                        onChange={(e) => updateParam('initial_prompt', e.target.value || undefined)}
                        className={`${inputClassName} resize-none min-h-[80px]`}
                        rows={2}
                    />
                </FormField>

                <p className="text-xs leading-5 text-[var(--text-secondary)]">
                    WhisperX applies the initial prompt and vocabulary independently to each audio chunk. Its batched runtime does not support conditioning on the previous chunk’s transcript.
                </p>

                <SwitchField id="suppress_numerals" label="Suppress numerals (write numbers as words)" description="Useful for readable prose. Leave off when exact numeric output matters for timestamps, measurements, prices, or identifiers." checked={params.suppress_numerals} onCheckedChange={(v) => updateParam('suppress_numerals', v)} />

                <div className="pt-2 border-t border-[var(--border-subtle)] space-y-4">
                    <SwitchField id="no_align" label="Skip word alignment (faster, less precise timestamps)" description="Turn on to save time/VRAM or debug failures. Leave off when you need word-level timing." checked={params.no_align} onCheckedChange={(v) => updateParam('no_align', v)} />

                    {!params.no_align && (
                        <FormField label="Custom Alignment Model" description="WhisperX-compatible alignment model (e.g., KBLab/wav2vec2-large-voxrex-swedish). Leave empty for default." optional>
                            <Input
                                placeholder="model/path or HuggingFace ID"
                                value={params.align_model || ""}
                                onChange={(e) => updateParam('align_model', e.target.value || undefined)}
                                className={inputClassName}
                            />
                        </FormField>
                    )}
                </div>
            </AdvancedAccordion>
        </div>
    );
}

function ParakeetConfig({ params, updateParam, isMultiTrack }: ConfigProps) {
    return (
        <div className="space-y-6">
            <Section title="Model Settings">
                <SelectField label="Transcription device" description={devicePolicyDescription(params.recovery_mode)} value={params.device} onValueChange={(value) => updateParam('device', value)} options={[{ value: "cpu", label: "CPU" }, { value: "cuda", label: "GPU (CUDA)" }, { value: "auto", label: params.recovery_mode ? "Auto · choose once" : "Auto · GPU, then CPU" }]} />
                <p className="mt-2 text-xs text-[var(--text-secondary)]">Parakeet uses FP32 weights. CUDA enables TF32 math; FP16 is not supported by this runtime.</p>
            </Section>
            <Section title="Audio Context" description="Configure how much context the model uses for long audio files">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-6">
                    <SliderField label="Left Context" value={params.attention_context_left} onValueChange={(v) => updateParam('attention_context_left', v)} min={64} max={512} step={64} />
                    <SliderField label="Right Context" value={params.attention_context_right} onValueChange={(v) => updateParam('attention_context_right', v)} min={64} max={512} step={64} />
                </div>
            </Section>

            {!isMultiTrack && (
                <DiarizationSection id="parakeet_diarize" params={params} updateParam={updateParam} />
            )}

            <AdvancedAccordion>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <FormField label="Batch Size" description={PARAM_DESCRIPTIONS.batch_size}>
                        <Input
                            type="number" min={1} max={8}
                            value={params.batch_size}
                            onChange={(e) => updateParam('batch_size', parseInt(e.target.value) || 1)}
                            className={inputClassName}
                        />
                    </FormField>
                    <FormField label="Long Audio Chunk" description={PARAM_DESCRIPTIONS.nvidia_chunk_duration}>
                        <Input
                            type="number" min={30} max={1800} step={30}
                            value={params.nvidia_chunk_duration || 300}
                            onChange={(e) => updateParam('nvidia_chunk_duration', parseInt(e.target.value) || 300)}
                            className={inputClassName}
                        />
                    </FormField>
                </div>
                <SwitchField
                    id="parakeet_timestamps"
                    label="Timestamp output"
                    description={PARAM_DESCRIPTIONS.nvidia_timestamps}
                    checked={params.nvidia_timestamps ?? true}
                    onCheckedChange={(v) => updateParam('nvidia_timestamps', v)}
                />
            </AdvancedAccordion>
        </div>
    );
}

function CanaryConfig({ params, updateParam, isMultiTrack }: ConfigProps) {
    return (
        <div className="space-y-6">
            <Section title="Language Settings">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <SelectField label="Task" description={PARAM_DESCRIPTIONS.task} value={params.task} onValueChange={(v) => updateParam('task', v)} options={[{ value: "transcribe", label: "Transcribe" }, { value: "translate", label: "Translate" }]} />
                    <SelectField label="Source Language" description={PARAM_DESCRIPTIONS.language} value={params.language || "en"} onValueChange={(v) => updateParam('language', v)} options={CANARY_LANGUAGES} />
                    {params.task === "translate" && (
                        <SelectField label="Target Language" description={PARAM_DESCRIPTIONS.task} value={params.nvidia_target_language || "en"} onValueChange={(v) => updateParam('nvidia_target_language', v)} options={CANARY_LANGUAGES} />
                    )}
                </div>
            </Section>

            {!isMultiTrack && (
                <DiarizationSection id="canary_diarize" params={params} updateParam={updateParam} />
            )}

            <AdvancedAccordion>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <SelectField
                        label="Device"
                        description={devicePolicyDescription(params.recovery_mode)}
                        value={params.device || "auto"}
                        onValueChange={(v) => updateParam('device', v)}
                        options={[{ value: "auto", label: params.recovery_mode ? "Auto · choose once" : "Auto · GPU, then CPU" }, { value: "cuda", label: "GPU (CUDA)" }, { value: "cpu", label: "CPU" }]}
                    />
                    <SelectField
                        label="Precision"
                        description={PARAM_DESCRIPTIONS.nvidia_precision}
                        value={params.nvidia_precision || "float16"}
                        onValueChange={(v) => updateParam('nvidia_precision', v)}
                        options={[{ value: "float16", label: "Float16" }, { value: "bfloat16", label: "BFloat16" }, { value: "float32", label: "Float32" }]}
                    />
                    <FormField label="Batch Size" description={PARAM_DESCRIPTIONS.batch_size}>
                        <Input
                            type="number" min={1} max={8}
                            value={params.batch_size}
                            onChange={(e) => updateParam('batch_size', parseInt(e.target.value) || 1)}
                            className={inputClassName}
                        />
                    </FormField>
                </div>
                <SwitchField
                    id="canary_chunking"
                    label="Chunk long audio"
                    description={PARAM_DESCRIPTIONS.nvidia_use_chunking}
                    checked={params.nvidia_use_chunking ?? false}
                    onCheckedChange={(v) => updateParam('nvidia_use_chunking', v)}
                />
                {(params.nvidia_use_chunking ?? false) && (
                    <FormField label="Chunk Length" description={PARAM_DESCRIPTIONS.nvidia_chunk_duration}>
                        <Input
                            type="number" min={10} max={300} step={10}
                            value={params.nvidia_chunk_duration || 40}
                            onChange={(e) => updateParam('nvidia_chunk_duration', parseInt(e.target.value) || 40)}
                            className={inputClassName}
                        />
                    </FormField>
                )}
                <SwitchField
                    id="canary_timestamps"
                    label="Timestamp output"
                    description={PARAM_DESCRIPTIONS.nvidia_timestamps}
                    checked={params.nvidia_timestamps ?? true}
                    onCheckedChange={(v) => updateParam('nvidia_timestamps', v)}
                />
            </AdvancedAccordion>
        </div>
    );
}

function CanaryQwenConfig({ params, updateParam, isMultiTrack }: ConfigProps) {
    return (
        <div className="space-y-6">
            <InfoBanner variant="warning" title="Chunk-Level Timing">
                Canary-Qwen returns text with chunk-level segments, not word-level timestamps.
            </InfoBanner>

            <Section title="Language Settings">
                <SelectField label="Language" value={params.language || "en"} onValueChange={(v) => updateParam('language', v)} options={CANARY_QWEN_LANGUAGES} />
            </Section>

            {!isMultiTrack && (
                <DiarizationSection id="canary_qwen_diarize" params={params} updateParam={updateParam} />
            )}

            <AdvancedAccordion>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <FormField label="Batch Size" description={PARAM_DESCRIPTIONS.batch_size}>
                        <Input
                            type="number" min={1} max={8}
                            value={params.batch_size}
                            onChange={(e) => updateParam('batch_size', parseInt(e.target.value) || 1)}
                            className={inputClassName}
                        />
                    </FormField>
                    <FormField label="Chunk Length" description={PARAM_DESCRIPTIONS.nvidia_chunk_duration}>
                        <Input
                            type="number" min={10} max={120} step={5}
                            value={params.nvidia_chunk_duration || 40}
                            onChange={(e) => updateParam('nvidia_chunk_duration', parseInt(e.target.value) || 40)}
                            className={inputClassName}
                        />
                    </FormField>
                    <FormField label="Max Tokens" description={PARAM_DESCRIPTIONS.max_new_tokens}>
                        <Input
                            type="number" min={64} max={2048} step={64}
                            value={params.max_new_tokens || 256}
                            onChange={(e) => updateParam('max_new_tokens', parseInt(e.target.value) || 256)}
                            className={inputClassName}
                        />
                    </FormField>
                    <SelectField
                        label="Precision"
                        description={PARAM_DESCRIPTIONS.nvidia_precision}
                        value={params.nvidia_precision || "float16"}
                        onValueChange={(v) => updateParam('nvidia_precision', v)}
                        options={[{ value: "float16", label: "Float16" }, { value: "bfloat16", label: "BFloat16" }, { value: "float32", label: "Float32" }]}
                    />
                    <SelectField
                        label="Device"
                        description={devicePolicyDescription(params.recovery_mode)}
                        value={params.device || "auto"}
                        onValueChange={(v) => updateParam('device', v)}
                        options={[{ value: "auto", label: params.recovery_mode ? "Auto · choose once" : "Auto · GPU, then CPU" }, { value: "cuda", label: "GPU (CUDA)" }, { value: "cpu", label: "CPU" }]}
                    />
                </div>

                <FormField label="Prompt" description={PARAM_DESCRIPTIONS.nvidia_prompt}>
                    <Textarea
                        value={params.nvidia_prompt || "Transcribe the following:"}
                        onChange={(e) => updateParam('nvidia_prompt', e.target.value || undefined)}
                        className={`${inputClassName} resize-none min-h-[80px]`}
                        rows={2}
                    />
                </FormField>

                <SwitchField
                    id="canary_qwen_timestamps"
                    label="Chunk timestamp output"
                    description={PARAM_DESCRIPTIONS.nvidia_timestamps}
                    checked={params.nvidia_timestamps ?? true}
                    onCheckedChange={(v) => updateParam('nvidia_timestamps', v)}
                />
            </AdvancedAccordion>
        </div>
    );
}

interface OpenAIConfigProps extends ConfigProps {
    isValidating: boolean;
    validationStatus: 'idle' | 'valid' | 'invalid';
    validationMessage: string;
    onValidate: () => void;
}

function OpenAIConfig({
    params, updateParam,
    isValidating, validationStatus, validationMessage, onValidate
}: OpenAIConfigProps) {
    return (
        <div className="space-y-6">
            <Section title="API Configuration">
                <div className="space-y-4">
                    <FormField label="OpenAI API Key" description="Your API key. Leave empty to use server default if configured.">
                        <div className="flex gap-2">
                            <Input
                                type="password" placeholder="sk-..."
                                value={params.api_key || ""}
                                onChange={(e) => updateParam('api_key', e.target.value)}
                                className={`${inputClassName} flex-1`}
                            />
                            <Button
                                variant="outline" onClick={onValidate} disabled={isValidating}
                                className="shrink-0 rounded-xl border-[var(--border-subtle)] cursor-pointer"
                            >
                                {isValidating ? <Loader2 className="h-4 w-4 animate-spin" /> : "Validate"}
                            </Button>
                        </div>
                        {validationStatus !== 'idle' && (
                            <div className={`flex items-center gap-2 text-sm mt-2 ${validationStatus === 'valid' ? 'text-[var(--success-solid)]' : 'text-[var(--error)]'}`}>
                                {validationStatus === 'valid' ? <Check className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
                                <span>{validationMessage}</span>
                            </div>
                        )}
                    </FormField>

                    <SelectField label="Language" value={params.language || "auto"} onValueChange={(v) => updateParam('language', v === "auto" ? undefined : v)} options={LANGUAGES} />
                </div>
            </Section>

            {params.model && params.model !== "whisper-1" && (
                <InfoBanner variant="warning" title="Limited Features">
                    Word-level timestamps are only supported by whisper-1. Synchronized playback won't be available.
                </InfoBanner>
            )}
        </div>
    );
}

function VoxtralConfig({ params, updateParam }: ConfigProps) {
    return (
        <div className="space-y-6">
            <InfoBanner variant="warning" title="Limited Features">
                Voxtral does not support word-level timestamps. Synchronized playback, audio seeking, and timestamp-based features won't be available.
            </InfoBanner>

            <Section title="Language Settings">
                <SelectField label="Language" description="Source language for transcription" value={params.language || "en"} onValueChange={(v) => updateParam('language', v)} options={LANGUAGES} />
            </Section>

            <AdvancedAccordion>
                <FormField label="Max Tokens" description="Maximum number of tokens to generate. Voxtral has a 32k context window and handles up to 30-40 minutes of audio.">
                    <Input
                        type="number" min={1024} max={16384}
                        value={params.max_new_tokens || 8192}
                        onChange={(e) => updateParam('max_new_tokens', parseInt(e.target.value) || 8192)}
                        className={inputClassName}
                    />
                </FormField>
            </AdvancedAccordion>
        </div>
    );
}

function ModelComparisonDetails({ model, variant, device, precision, batchSize }: { model: TranscriptionModelCapability; variant: string; device: string; precision: string; batchSize: number }) {
    const metadata = model.metadata ?? {};
    const applies = modelDetailsApply(model, variant);
    const memoryEstimate = modelMemoryEstimate(model, variant);
    const cpuRAM = memoryEstimate.cpuRAM;
    const cpuMemoryLabel = memoryEstimate.cpuPrecision;
    const gpu = gpuMemoryEstimate(memoryEstimate, device === "cuda" || device === "auto" ? precision : undefined);
    const amiWER = applies.benchmark ? metadata.benchmark_ami_wer : undefined;
    const conversationalWER = applies.benchmark ? metadata.benchmark_conversational_wer : undefined;
    const otherWER = additionalBenchmarks(model, variant).find((result) => result.metric.toUpperCase() === "WER");
    const source = metadata.benchmark_source;
    const sourceIsLink = source?.startsWith("https://");
    return (
        <div className="space-y-3 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-main)] p-4">

            {!amiWER && otherWER && <div className="text-sm leading-6">
                <p className="font-medium text-[var(--text-primary)]">Other published evaluation · WER {otherWER.value.toFixed(2)}% · {otherWER.dataset}</p>
                <p className="text-xs text-[var(--text-secondary)]">{otherWER.provenance === "community" ? "Community" : "Publisher"} result on a different test; details and source below.</p>
            </div>}
            <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
                <div><dt className="text-xs text-[var(--text-tertiary)]">Published WER · AMI-Cleaned meeting clips</dt><dd className="mt-1 font-medium text-[var(--text-primary)]">{amiWER ? `${amiWER}%` : "AMI result not available"}</dd></div>
                <div><dt className="text-xs text-[var(--text-tertiary)]">Published WER · private conversational test</dt><dd className="mt-1 font-medium text-[var(--text-primary)]">{conversationalWER ? `${conversationalWER}%` : "Conversational result not available"}</dd></div>
                <div><dt className="text-xs text-[var(--text-tertiary)]">Estimated CPU RAM ({cpuMemoryLabel})</dt><dd className="mt-1 font-medium text-[var(--text-primary)]">{cpuRAM ? `${cpuRAM} GB` : "Not estimated"}</dd></div>
                <div><dt className="text-xs text-[var(--text-tertiary)]">Estimated GPU VRAM ({gpu.precision})</dt><dd className="mt-1 font-medium text-[var(--text-primary)]">{memoryEstimate.gpuSupported === false ? "GPU not supported" : gpu.value ? `${gpu.value} GB` : "Not estimated"}</dd></div>
            </dl>
            <p className="text-xs leading-5 text-[var(--text-secondary)]">{memoryEstimate.gpuSupported !== false && referenceGPUFit(gpu.value)} Reference hardware: RTX 3060 12 GB VRAM; 32 GB system RAM available for models.</p>
            <p className="text-xs leading-5 text-[var(--text-secondary)]">ASR planning ranges for one worker, batch size 1 and short chunks. {batchSize > 1 ? `Your batch size is ${batchSize}; peak memory may be higher.` : "GPU runs also use system RAM for loading and audio."} {device === "auto" ? "Auto can use either device." : `Selected: ${device === "cuda" ? "GPU" : "CPU"} · ${precision}.`}</p>
            <details className="text-xs leading-5 text-[var(--text-secondary)]">
                <summary className="cursor-pointer font-medium">Benchmark sources and memory assumptions</summary>
                <div className="mt-2 space-y-2">
                    <p>{model.description}</p>
                    <p>Published WER measures word errors on AMI-Cleaned meeting clips and a private conversational test, not your recordings.
                        {metadata.benchmark_date && ` Snapshot: ${metadata.benchmark_date}.`}
                        {source && <> {sourceIsLink ? <a href={source} target="_blank" rel="noreferrer" className="underline underline-offset-2">Benchmark source</a> : <span>Source: {source}.</span>}</>}
                    </p>
                    <p>Working memory varies with precision, recording length, chunk size and alignment. Estimates are not guarantees.</p>
                    {memoryEstimate.notes && <p>CPU: {memoryEstimate.notes}</p>}
                    {memoryEstimate.gpuNotes && <p>GPU: {memoryEstimate.gpuNotes}</p>}
                    {memoryEstimate.gpuFloat32RAM && gpu.precision !== "FP32" && <p>GPU FP32 alternative: {memoryEstimate.gpuFloat32RAM} GB estimated.</p>}
                </div>
            </details>
            <OtherBenchmarkDetails model={model} variant={variant} />
        </div>
    );
}

function PipelineMemoryDetails({ params, models }: { params: WhisperXParams; models: TranscriptionModelCapability[] }) {
    const speaker = models.find((model) => model.model_id === (params.diarize_model === "nvidia_sortformer" ? "sortformer" : params.diarize_model));
    const speakerMemory = speaker ? modelMemoryEstimate(speaker, params.diarization_checkpoint) : undefined;
    const speakerGPU = speakerMemory ? gpuMemoryEstimate(speakerMemory) : undefined;
    const requested = requestedDiarizationDevice(params.model_family, params.diarization_device);
    const speakerDevice = requested === "same" ? params.device : requested;
    const alignment = alignmentMemoryForConfiguration(params, models);
    const deviceLabel = (device: string) => device === "cuda" ? "GPU" : device === "cpu" ? "CPU" : "Auto";
    const value = (range?: string) => range ? `${range} GB estimated` : "not estimated";
    const stageMemory = (device: string, cpu?: string, gpu?: string) => device === "cpu"
        ? `CPU RAM ${value(cpu)}; this stage does not use GPU VRAM.`
        : device === "cuda" ? `GPU VRAM ${value(gpu)}; system RAM is also used for loading and audio.`
        : `CPU RAM ${value(cpu)} or GPU VRAM ${value(gpu)}, depending on the device used.`;
    return <div className="space-y-2 rounded-xl border border-[var(--border-subtle)] p-4 text-xs leading-5 text-[var(--text-secondary)]">
        <h4 className="text-sm font-medium text-[var(--text-primary)]">Memory for this configuration</h4>
        <p>Transcription: {deviceLabel(params.device)} · {transcriptionPrecision(params)} · batch {params.batch_size}.</p>
        {alignment?.enabled && <>
            <p>Word alignment: {deviceLabel(alignment.device)} · {alignment.precision}{alignment.fixed ? " · saved stage override" : " · initial settings"} · {stageMemory(alignment.device, alignment.memory.cpuRAM, alignment.gpuRAM)}</p>
            {alignment.memory.notes && <p>{alignment.memory.label}: {alignment.memory.notes}</p>}
            {alignment.cpuFallbackPrecision && <p>Alignment CPU fallback is permitted at {alignment.cpuFallbackPrecision}; it is used only if recovery requires it. CPU RAM {value(alignment.memory.cpuRAM)}.</p>}
        </>}
        {alignment && !alignment.enabled && <p>Word alignment is off.</p>}
        {!alignment && <p>Timing comes from the transcription model; no separate word-alignment stage is configured.</p>}
        {params.diarize ? params.diarize_model === "native" ? <p>Speaker identification is built into the transcription model.</p> : <p>Speaker identification: {speaker?.display_name || params.diarize_model} on {deviceLabel(speakerDevice)} · {stageMemory(speakerDevice, speakerMemory?.cpuRAM, speakerGPU?.value)}</p> : <p>Speaker identification is off.</p>}
        <p>Stages may run sequentially, so these estimates are not added into a single peak. Long recordings, larger batches and concurrent jobs can exceed the ranges.</p>
    </div>;
}

function DiarizationComparisonDetails({ model, device, checkpoint }: { model: TranscriptionModelCapability; device: string; checkpoint?: string }) {
    const metadata = model.metadata ?? {};
    const otherDER = additionalBenchmarks(model, checkpoint).find((result) => result.metric.toUpperCase() === "DER");
    const benchmarkApplies = diarizationBenchmarkApplies(model, checkpoint);
    const memory = modelMemoryEstimate(model, checkpoint);
    const gpu = gpuMemoryEstimate(memory);
    return (
        <div className="space-y-2 text-xs leading-5 text-[var(--text-secondary)]">
            {!(benchmarkApplies && metadata.benchmark_der) && otherDER ? <>
                <p><strong className="text-[var(--text-primary)]">{otherDER.provenance === "community" ? "Community" : "Publisher"} DER {otherDER.value}% · {otherDER.dataset}</strong></p>
                <p>Separate evaluation protocol; see notes and source below. DER measures speaker attribution, not word recognition.</p>
            </> : <p>Published speaker diarization error (DER){benchmarkApplies && metadata.benchmark_der_dataset ? ` · ${metadata.benchmark_der_dataset}` : ""}: <strong className="text-[var(--text-primary)]">{benchmarkApplies && metadata.benchmark_der ? `${metadata.benchmark_der}%` : "No comparable result"}</strong>. Lower is better; this measures speaker attribution, not word recognition.</p>}
            <p>Estimated CPU RAM ({memory.cpuPrecision}): <strong>{memory.cpuRAM ? `${memory.cpuRAM} GB` : "not estimated"}</strong> · Estimated GPU VRAM ({gpu.precision}): <strong>{memory.gpuSupported === false ? "unsupported" : gpu.value ? `${gpu.value} GB` : "not estimated"}</strong>.</p>
            <p>Speaker device: {device === "cuda" ? "GPU" : device === "cpu" ? "CPU" : "Auto"}. {memory.gpuNotes || memory.notes || "Working memory varies with recording length and settings."}</p>
            {benchmarkApplies && (metadata.benchmark_notes || metadata.benchmark_caveat) && <p>{metadata.benchmark_notes || metadata.benchmark_caveat}</p>}
            {benchmarkApplies && metadata.benchmark_source?.startsWith("https://") && <a href={metadata.benchmark_source} target="_blank" rel="noreferrer" className="underline underline-offset-2">Diarization benchmark source</a>}
            <OtherBenchmarkDetails model={model} variant={checkpoint} />
        </div>
    );
}

function LocalModelConfig({ params, updateParam, isMultiTrack, capability }: ConfigProps & { capability: TranscriptionModelCapability }) {
    const supportedLanguages = capability.supported_languages ?? [];
    const languages = supportedLanguages.length && !supportedLanguages.includes("*")
        ? supportedLanguages.map((value) => LANGUAGES.find((language) => language.value === value) ?? { value, label: value })
        : LANGUAGES;
    const integrated = capability.features.integrated_diarization === true;
    const allowedDevices = capability.metadata?.supported_devices?.split(",") || ["cpu", "cuda", "auto"];
    const fixedPrecision = capability.metadata?.fixed_precision;
    const fixedChunk = capability.metadata?.fixed_chunk_duration;
    const defaultChunk = capability.metadata?.default_chunk_duration || (integrated ? "0 (full recording)" : "30");
    return (
        <div className="space-y-6">
            <Section title="Model settings">
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    <SelectField label="Language" value={params.language || "auto"} onValueChange={(value) => updateParam('language', value === "auto" ? undefined : value)} options={languages} />
                    <SelectField label="Transcription device" description={devicePolicyDescription(params.recovery_mode)} value={params.device} onValueChange={(value) => updateParam('device', value)} options={[{ value: "cpu", label: "CPU" }, { value: "cuda", label: "GPU (CUDA)" }, { value: "auto", label: params.recovery_mode ? "Auto · choose once" : "Auto · GPU, then CPU" }].filter((option) => allowedDevices.includes(option.value))} />
                    {fixedPrecision ? <FormField label="Precision"><p className="py-2 text-sm text-[var(--text-secondary)]">Fixed quantized weights ({fixedPrecision})</p></FormField> : <SelectField label="Precision" value={params.compute_type} onValueChange={(value) => updateParam('compute_type', value)} options={[{ value: "float32", label: "Float32" }, { value: "bfloat16", label: "BFloat16" }, { value: "float16", label: "Float16" }]} />}
                </div>
            </Section>
            {!isMultiTrack && <DiarizationSection id="local-model-diarize" params={params} updateParam={updateParam} integrated={integrated} />}
            <AdvancedAccordion>
                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    {fixedChunk ? <FormField label="Audio chunk duration"><p className="py-2 text-sm text-[var(--text-secondary)]">{fixedChunk} seconds (fixed by the model runtime)</p></FormField> : <FormField label="Audio chunk duration (seconds)" description={integrated ? "The default keeps the full recording together to preserve speaker identity. Splitting a recording can reset speaker labels between chunks." : "Leave blank for the model default. Shorter chunks use less memory but reduce the context available to the model."} optional>
                        <Input type="number" min={0} max={Number(capability.metadata?.max_chunk_duration || (integrated ? "5400" : "30"))} placeholder={`Model default: ${defaultChunk}`} value={params.audio_chunk_duration ?? ""} onChange={(event) => updateParam('audio_chunk_duration', event.target.value === "" ? null : Number(event.target.value))} className={inputClassName} />
                    </FormField>}
                    <FormField label="Maximum generated tokens" description="Leave blank for the model default. Increase this only if a transcript is cut off before the audio ends." optional>
                        <Input type="number" min={0} max={65536} placeholder={capability.metadata?.default_max_new_tokens || "Model default"} value={params.max_new_tokens ?? ""} onChange={(event) => updateParam('max_new_tokens', event.target.value === "" ? undefined : Number(event.target.value))} className={inputClassName} />
                    </FormField>
                </div>
                <SwitchField id="local-word-alignment" label="Align words to the audio" description="Adds word timing for synchronized playback and external speaker attribution." checked={!params.no_align} onCheckedChange={(value) => updateParam('no_align', !value)} />
            </AdvancedAccordion>
        </div>
    );
}

function HFTokenOverride({ params, updateParam, hasSavedToken, requiresToken }: ConfigProps & { hasSavedToken: boolean; requiresToken: boolean }) {
    const source = hfTokenSource(params);
    return (
        <details open={requiresToken || undefined} className="rounded-xl border border-[var(--border-subtle)] p-4 text-sm">
            <summary className="cursor-pointer font-medium text-[var(--text-primary)]">Hugging Face access · {source === "default" ? "saved default" : source === "custom" ? "custom token" : "saved token disabled"}</summary>
            <div className="mt-4 space-y-4">
                <SelectField label="Hugging Face token" value={source} onValueChange={(value) => {
                    updateParam('hf_token_source', value as "default" | "custom" | "none");
                    updateParam('hf_token', undefined);
                }} options={[
                    { value: "default", label: "Use saved default" },
                    { value: "custom", label: "Use a custom token" },
                    { value: "none", label: "Do not use a saved token" },
                ]} />
                {source === "default" && <p className="text-xs text-[var(--text-secondary)]">{hasSavedToken ? "A default token is saved in Settings." : "No default token is saved. Add one in Settings for gated model downloads."}</p>}
                {source === "custom" && <FormField label="Custom Hugging Face token" htmlFor="custom-hf-token">
                    <Input id="custom-hf-token" type="password" autoComplete="new-password" value={params.hf_token || ""}
                        placeholder={params.has_hf_token && !requiresToken ? "Token saved · enter a replacement" : "hf_..."}
                        onChange={(event) => updateParam('hf_token', event.target.value || undefined)} className={inputClassName} />
                </FormField>}
                {requiresToken && <p role="alert" className="text-xs leading-5 text-[var(--warning-solid)]">Enter a custom token for this configuration, or choose the saved default. Saved token values are hidden and cannot be copied from a previous run.</p>}
                <p className="text-xs leading-5 text-[var(--text-secondary)]">Used to download gated models from Hugging Face. It does not upload your audio. Existing server credentials or cached models may still provide access.</p>
            </div>
        </details>
    );
}

function OtherBenchmarkDetails({ model, variant }: { model: TranscriptionModelCapability; variant?: string }) {
    const results = additionalBenchmarks(model, variant);
    if (!results.length) return null;
    return <details className="text-xs leading-5 text-[var(--text-secondary)]">
        <summary className="cursor-pointer font-medium">Other published evaluations ({results.length})</summary>
        <div className="mt-2 space-y-3">
            <p>Different datasets and protocols; these results do not affect the model ranking above.</p>
            {results.map((result, index) => <div key={`${result.dataset}-${index}`}>
                <p className="font-medium text-[var(--text-primary)]">{result.dataset} · {result.metric} {result.value}%</p>
                <p>{result.provenance === "community" ? "Community evaluation" : "Publisher evaluation"}
                    {" · "}{result.source.startsWith("https://") ? <a href={result.source} target="_blank" rel="noreferrer" className="underline underline-offset-2">{result.source_label || "Source"}</a> : result.source_label || "Source not linked"}
                </p>
                {result.notes && <p>{result.notes}</p>}
            </div>)}
        </div>
    </details>;
}
