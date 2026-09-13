import assert from "node:assert/strict";
import test from "node:test";
import { gpuMemoryEstimate, referenceGPUFit, alignmentMemoryEstimate, modelMemoryEstimate, additionalBenchmarks, diarizationBenchmarkApplies, hfTokenSource, needsCustomHFToken, WHISPER_CHECKPOINTS, transcriptionModelChoices, sortModelChoices, modelChoiceMetrics, modelChoiceValue, modelExecutionLocation, selectTranscriptionModel, normalizeModelCapabilities, modelVariants, contextSupport, findModelCapability, modelFamilyOptions, modelDetailsApply, requestedDiarizationDevice, selectCatalogModel, transcriptionModelLabel, transcriptionPrecision, type TranscriptionModelCapability } from "./modelCapabilities.ts";

const model = (changes: Partial<TranscriptionModelCapability> = {}): TranscriptionModelCapability => ({
    model_id: "org/model-a", model_family: "new_asr", display_name: "Model A", description: "", features: {}, ...changes,
});

test("variant context support takes precedence over family fallback", () => {
    const models = [model({ features: { context: true } }), model({ model_id: "org/model-b" })];
    assert.deepEqual(contextSupport(findModelCapability(models, "new_asr", "org/model-b")), { prose: false, terms: false });
    assert.deepEqual(contextSupport(findModelCapability(models, "new_asr", "org/model-a")), { prose: true, terms: true });
});

test("keyword-only models do not present prose context as supported", () => {
    assert.deepEqual(contextSupport(model({ features: { context: true }, metadata: { context_mode: "terms" } })), { prose: false, terms: true });
    assert.deepEqual(contextSupport(undefined), { prose: false, terms: false });
});

test("new ASR families appear once and standalone diarizers stay out of ASR selection", () => {
    const options = modelFamilyOptions([
        model(), model({ model_id: "org/model-b" }),
        model({ model_id: "pyannote", model_family: "pyannote" }),
        model({ model_id: "sortformer", model_family: "nvidia_sortformer" }),
    ]);
    assert.equal(options.filter((item) => item.value === "new_asr").length, 1);
    assert.equal(options.some((item) => item.value === "pyannote" || item.value === "nvidia_sortformer"), false);
    assert.equal(options.some((item) => item.value === "whisper"), true);
});

test("run labels distinguish variants of newly registered families", () => {
    assert.equal(transcriptionModelLabel("new_asr", "org/model-a"), "model-a");
    assert.equal(transcriptionModelLabel("new_asr", "org/model-b"), "model-b");
    assert.equal(transcriptionModelLabel("whisper", "large-v3"), "Whisper large-v3");
});

test("Whisper large-v3 estimates are not presented as small model measurements", () => {
    const capability = model({ metadata: { benchmark_model: "large-v3", memory_model: "large-v3" } });
    assert.deepEqual(modelDetailsApply(capability, "small"), { benchmark: false, memory: false });
    assert.deepEqual(modelDetailsApply(capability, "large-v3"), { benchmark: true, memory: true });
});

test("saved legacy profiles retain their former diarization device behavior", () => {
    assert.equal(requestedDiarizationDevice("nvidia_canary", undefined), "auto");
    assert.equal(requestedDiarizationDevice("nvidia_canary", ""), "auto");
    assert.equal(requestedDiarizationDevice("whisper", ""), "same");
    assert.equal(requestedDiarizationDevice(undefined, undefined), "same");
    assert.equal(requestedDiarizationDevice("qwen3_asr", ""), "same");
    assert.equal(requestedDiarizationDevice("nvidia_canary", "same"), "same");
    assert.equal(requestedDiarizationDevice("whisper", "cpu"), "cpu");
});


test("public API catalog names normalize without losing comparison metadata", () => {
    const result = normalizeModelCapabilities({ "org/model-a": { id: "org/model-a", family: "new_asr", name: "Model A", languages: ["en"], memory_mb: 12000, features: { context: true }, metadata: { benchmark_ami_wer: "8.31" } } });
    assert.equal(result[0].model_family, "new_asr");
    assert.equal(result[0].display_name, "Model A");
    assert.equal(result[0].memory_requirement_mb, 12000);
    assert.equal(result[0].metadata?.benchmark_ami_wer, "8.31");
    assert.deepEqual(result[0].supported_languages, ["en"]);
});

test("legacy aliases do not create duplicate variant choices", () => {
    const entry = { id: "org/model-a", family: "new_asr", name: "Model A" };
    assert.equal(normalizeModelCapabilities({ legacy: entry, "org/model-a": entry }).length, 1);
    assert.equal(modelFamilyOptions([model({ model_family: "suplime" }), model({ model_family: "diarizen" })]).some((option) => ["suplime", "diarizen"].includes(option.value)), false);
    assert.deepEqual(modelVariants(model({ metadata: { model_variants: '["rewayai/suplime","rewayai/suplime-large"]' } })), ["rewayai/suplime", "rewayai/suplime-large"]);
    assert.deepEqual(modelVariants(model({ metadata: { model_variants: 'invalid' } })), []);
});

const selectionParams = {
    model_family: "whisper", model: "large-v3", device: "cpu", compute_type: "int8",
    task: "transcribe", batch_size: 8, language: "en", audio_chunk_duration: 25,
    max_new_tokens: 1024, diarize: true, diarize_model: "diarizen",
    diarization_checkpoint: "BUT-FIT/diarizen-wavlm-large-s80-md",
    transcription_context: "Engineering meeting", beam_size: 5,
};

test("NVIDIA memory estimates use the runtime precision field", () => {
    for (const model_family of ["nvidia_canary", "nvidia_canary_qwen"]) {
        assert.equal(transcriptionPrecision({ model_family, compute_type: "float32", nvidia_precision: "float16" }), "float16");
        assert.equal(transcriptionPrecision({ model_family, compute_type: "int8", nvidia_precision: "bfloat16" }), "bfloat16");
        assert.equal(transcriptionPrecision({ model_family, compute_type: "float16", nvidia_precision: "float32" }), "float32");
        assert.equal(transcriptionPrecision({ model_family, compute_type: "float32" }), "float16");
    }
    assert.equal(transcriptionPrecision({ model_family: "qwen3_asr", compute_type: "float32", nvidia_precision: "float16" }), "float32");
    assert.equal(transcriptionPrecision({ model_family: "whisper", compute_type: "int8", nvidia_precision: "float16" }), "int8");
});

test("Whisper int8 to Voxtral selects a catalog checkpoint and valid CPU precision", () => {
    const voxtral = model({ model_family: "mistral_voxtral", model_id: "mistralai/Voxtral-Mini-3B-2507", supported_languages: ["en", "fr"] });
    const params = { ...selectionParams };
    const selected = selectCatalogModel(params, "model_family", "mistral_voxtral", [voxtral]);
    assert.ok(selected);
    assert.equal(selected.model, voxtral.model_id);
    assert.equal(selected.model_family, "mistral_voxtral");
    assert.equal(selected.device, "cpu");
    assert.equal(selected.compute_type, "float32");
    assert.equal(selected.batch_size, 1);
    assert.equal(selected.audio_chunk_duration, null);
    assert.equal(selected.max_new_tokens, undefined);
    assert.equal(selected.diarize, true);
    assert.equal(selected.diarize_model, params.diarize_model);
    assert.equal(selected.diarization_checkpoint, params.diarization_checkpoint);
    assert.equal(selected.transcription_context, params.transcription_context);
    assert.equal(selected.beam_size, params.beam_size);
    assert.deepEqual(params, selectionParams);
    assert.equal(selectCatalogModel(params, "model", "medium", [voxtral]), undefined);
});

test("non-native ASR variant changes preserve external diarization and its checkpoint", () => {
    for (const family of ["qwen3_asr", "ibm_granite_speech"]) {
        const smaller = model({ model_family: family, model_id: "org/smaller", supported_languages: ["auto", "en"] });
        const larger = model({ model_family: family, model_id: "org/larger", supported_languages: ["auto", "en"] });
        for (const diarize of [true, false]) {
            const params = { ...selectionParams, model_family: family, model: smaller.model_id, compute_type: "float32", diarize };
            const selected = selectCatalogModel(params, "model", larger.model_id, [smaller, larger]);
            assert.ok(selected);
            assert.equal(selected.model, larger.model_id);
            assert.equal(selected.diarize, diarize);
            assert.equal(selected.diarize_model, params.diarize_model);
            assert.equal(selected.diarization_checkpoint, params.diarization_checkpoint);
            assert.equal(selected.transcription_context, params.transcription_context);
        }
    }
});

test("native speaker defaults apply only on entering native ASR and respect multi-track audio", () => {
    const plain = model();
    const native = model({ model_family: "moss_asr", model_id: "OpenMOSS/MOSS-Transcribe-Diarize-0.9B", features: { integrated_diarization: true } });
    const models = [plain, native];
    const selected = selectCatalogModel(selectionParams, "model_family", native.model_family, models);
    assert.ok(selected);
    assert.equal(selected.diarize, true);
    assert.equal(selected.diarize_model, "native");
    assert.equal(selected.diarization_checkpoint, undefined);
    const leaving = selectCatalogModel(selected, "model_family", plain.model_family, models);
    assert.equal(leaving?.diarize, true);
    assert.equal(leaving?.diarize_model, "pyannote");
    assert.equal(leaving?.diarization_checkpoint, undefined);
    assert.equal(selectCatalogModel(selectionParams, "model_family", native.model_family, models, true)?.diarize, false);
    const external = { ...selected, diarize_model: "diarizen", diarization_checkpoint: selectionParams.diarization_checkpoint };
    assert.deepEqual(selectCatalogModel(external, "model", native.model_id, models), external);
});

// Selection order and privacy labels are checked independently of component markup.
test("global ranking compares checkpoints across families and leaves unknown variants last", () => {
    const models = [
        model({ model_family: "family-a", metadata: { benchmark_ami_wer: "8.31", benchmark_conversational_wer: "12.14", cpu_float32_ram_gb: "13–17", execution_location: "local" } }),
        model({ model_id: "org/model-b", model_family: "family-b", metadata: { benchmark_ami_wer: "5.90", benchmark_conversational_wer: "14.00", cpu_float32_ram_gb: "8–11", execution_location: "local" } }),
        model({ model_id: "whisper", model_family: "whisper", metadata: { benchmark_ami_wer: "11.02", benchmark_model: "large-v3", memory_model: "large-v3", cpu_float32_ram_gb: "10–14" } }),
    ];
    const choices = transcriptionModelChoices(models);
    assert.equal(choices.filter((choice) => choice.family === "whisper").length, WHISPER_CHECKPOINTS.length);
    const ordered = sortModelChoices(choices, "meeting");
    assert.deepEqual(ordered.slice(0, 3).map((choice) => choice.model), ["org/model-b", "org/model-a", "large-v3"]);
    assert.equal(ordered.at(-1)?.family, "openai");
    assert.equal(choices.find((choice) => choice.model === "small")?.meetingWER, undefined);
    assert.equal(choices.find((choice) => choice.model === "small")?.cpuRAM, undefined);
    assert.equal(sortModelChoices(choices, "conversational")[0].model, "org/model-a");
    assert.equal(sortModelChoices(choices, "memory")[0].model, "org/model-b");
    assert.match(modelChoiceMetrics(choices.find((choice) => choice.model === "org/model-b")!), /5.90%.*CPU FP32 RAM est. 8–11 GB/);
});

test("all choices disclose location and retain cloud APIs with distinct local Whisper labels", () => {
    const unknown = model();
    const choices = transcriptionModelChoices([unknown], { model_family: "openai", model: "gpt-4o-transcribe" });
    assert.equal(choices.find((choice) => choice.model === "small")?.label, "Local · OpenAI Whisper small");
    assert.match(choices.find((choice) => choice.model === "whisper-1")!.label, /^Cloud · OpenAI API/);
    assert.match(modelChoiceMetrics(choices.find((choice) => choice.model === "whisper-1")!), /uploads audio to OpenAI/);
    assert.equal(choices.find((choice) => choice.model === unknown.model_id)?.location, "unknown");
    assert.equal(choices.find((choice) => choice.model === "gpt-4o-transcribe")?.location, "cloud");
    assert.equal(modelExecutionLocation("nvidia_canary"), "local");
});

test("saved aliases remain selected without duplicate catalog IDs or borrowed Whisper metrics", () => {
    const voxtral = model({ model_id: "mistralai/Voxtral-Mini-3B-2507", model_family: "mistral_voxtral" });
    const choices = transcriptionModelChoices([voxtral, voxtral], { model_family: "mistral_voxtral", model: "voxtral" });
    assert.equal(choices.filter((choice) => choice.family === "mistral_voxtral").length, 1);
    assert.ok(choices.find((choice) => choice.value === modelChoiceValue("mistral_voxtral", "voxtral")));
    const whisper = model({ model_family: "whisper", model_id: "whisper", metadata: { benchmark_ami_wer: "11.02", benchmark_model: "large-v3", memory_model: "large-v3", cpu_float32_ram_gb: "10–14" } });
    const turbo = transcriptionModelChoices([whisper], { model_family: "whisper", model: "turbo" }).find((choice) => choice.model === "turbo")!;
    assert.equal(turbo.meetingWER, undefined); assert.equal(turbo.cpuRAM, undefined);
});

test("unified checkpoint selection preserves context and external speakers across local families", () => {
    const smaller = model({ model_family: "qwen3_asr", metadata: { execution_location: "local" } });
    const native = model({ model_family: "moss_asr", model_id: "org/native", features: { integrated_diarization: true } });
    const models = [smaller, native];
    for (const target of ["org/model-a", "org/native", "large-v3"]) {
        const choice = transcriptionModelChoices(models).find((choice) => choice.model === target)!;
        const result = selectTranscriptionModel(selectionParams, choice, models);
        assert.equal(result.model, target);
        assert.equal(result.diarize, true);
        assert.equal(result.diarize_model, selectionParams.diarize_model);
        assert.equal(result.diarization_checkpoint, selectionParams.diarization_checkpoint);
        assert.equal(result.transcription_context, selectionParams.transcription_context);
    }
    const choices = transcriptionModelChoices(models);
    const cloud = choices.find((choice) => choice.family === "openai")!;
    const cloudProfile = selectTranscriptionModel(selectionParams, cloud, models);
    assert.equal(cloudProfile.model_family, "openai");
    assert.equal(cloudProfile.model, "whisper-1");
    assert.equal(cloudProfile.diarize, false);
    const local = selectTranscriptionModel(cloudProfile, choices.find((choice) => choice.model === smaller.model_id)!, models);
    assert.equal(local.device, "cpu"); assert.equal(local.compute_type, "float32");
});


test("cloud transition clears native-only speakers and multi-track stays off", () => {
    const cloud = transcriptionModelChoices([]).find((choice) => choice.family === "openai")!;
    const params = { ...selectionParams, model_family: "moss_asr", model: "org/native", diarize_model: "native" };
    const result = selectTranscriptionModel(params, cloud, []);
    assert.equal(result.diarize, false); assert.equal(result.diarize_model, "pyannote");
    assert.equal(result.diarization_checkpoint, undefined);
    assert.equal(selectTranscriptionModel(selectionParams, cloud, [], true).diarize, false);
});

test("write-only custom tokens require replacement for runs but can be preserved by profile edits", () => {
    assert.equal(hfTokenSource({}), "default");
    assert.equal(hfTokenSource({ has_hf_token: true }), "custom");
    assert.equal(needsCustomHFToken({ hf_token_source: "custom", has_hf_token: true }, false), true);
    assert.equal(needsCustomHFToken({ hf_token_source: "custom", has_hf_token: true }, true), false);
    assert.equal(needsCustomHFToken({ hf_token_source: "custom" }, true), true);
    assert.equal(needsCustomHFToken({ hf_token_source: "custom", hf_token: " " }, false), true);
    assert.equal(needsCustomHFToken({ hf_token_source: "custom", hf_token: "hf_example" }, false), false);
    assert.equal(needsCustomHFToken({ hf_token_source: "default", has_hf_token: true }, false), false);
    assert.equal(needsCustomHFToken({ hf_token_source: "none", has_hf_token: true }, false), false);
});

test("supplementary evaluations match exact checkpoints and never change the comparable ranking", () => {
    const whisper = model({ model_id: "whisper", model_family: "whisper", metadata: {
        benchmark_model: "large-v3", benchmark_ami_wer: "11.02",
        additional_benchmarks: JSON.stringify([
            { model: "large-v3", metric: "WER", dataset: "Other meeting test", value: 4.5, source: "https://example.test/source", provenance: "community" },
        ]),
    } });
    assert.equal(additionalBenchmarks(whisper, "small").length, 0);
    assert.equal(additionalBenchmarks(whisper, "large-v3").length, 1);
    const choices = transcriptionModelChoices([whisper]);
    assert.equal(choices.find((choice) => choice.model === "large-v3")?.meetingWER, 11.02);
    assert.equal(additionalBenchmarks(model({ metadata: { additional_benchmarks: "invalid" } })).length, 0);
});

test("Community-1 DER never appears on legacy pyannote3.1", () => {
    const pyannote = model({ model_id: "pyannote", model_family: "pyannote", metadata: {
        model_id: "pyannote/speaker-diarization-community-1", benchmark_der: "19.9",
        additional_benchmarks: JSON.stringify([{ model: "pyannote/speaker-diarization-3.1", metric: "DER", dataset: "AMI SDM", value: 22, source: "https://example.test/source" }]),
    } });
    assert.equal(diarizationBenchmarkApplies(pyannote), true);
    assert.equal(diarizationBenchmarkApplies(pyannote, "pyannote/speaker-diarization-3.1"), false);
    assert.equal(additionalBenchmarks(pyannote).length, 0);
    assert.equal(additionalBenchmarks(pyannote, "pyannote/speaker-diarization-3.1").length, 1);
});


test("meeting recommendations rank exact checkpoints independently of raw WER", () => {
    const choices = transcriptionModelChoices([
        model({ model_id: "org/recommended", metadata: { meeting_recommendation_rank: "1", meeting_recommendation_reason: "Context support for engineering meetings", benchmark_ami_wer: "8.31" } }),
        model({ model_id: "org/lower-wer", metadata: { meeting_recommendation_rank: "2", benchmark_ami_wer: "5.90" } }),
        model({ model_family: "whisper", model_id: "whisper", metadata: { meeting_recommendation_rank: "0", meeting_recommendations: JSON.stringify([{ model: "large-v3", rank: 3, reason: "Established baseline" }]) } }),
    ]);
    assert.equal(sortModelChoices(choices)[0].model, "org/recommended");
    assert.equal(sortModelChoices(choices, "meeting")[0].model, "org/lower-wer");
    assert.equal(choices.find((choice) => choice.model === "small")?.recommendationRank, undefined);
    assert.equal(choices.find((choice) => choice.model === "large-v3")?.recommendationRank, 3);
    assert.match(modelChoiceMetrics(sortModelChoices(choices)[0]), /^#1/);
    assert.equal(sortModelChoices(choices).at(-1)?.family, "openai");
});


test("Whisper variant estimates supply memory without borrowing large-v3 benchmark scores", () => {
    const whisper = model({ model_family: "whisper", model_id: "whisper", metadata: {
        benchmark_model: "large-v3", benchmark_ami_wer: "11.02", memory_model: "large-v3", cpu_float32_ram_gb: "10–14",
        variant_memory_estimates: JSON.stringify([
            { model: "tiny", cpu_float32_ram_gb: "2–4", gpu_float16_vram_gb: "1–3", gpu_float32_vram_gb: "2–4", notes: "Unmeasured planning range" },
            { model: "small", cpu_float32_ram_gb: "3–6", notes: "Small checkpoint estimate" },
        ]),
    } });
    const tiny = transcriptionModelChoices([whisper]).find((choice) => choice.model === "tiny")!;
    assert.equal(tiny.cpuRAM, "2–4"); assert.equal(tiny.meetingWER, undefined);
    assert.deepEqual(modelMemoryEstimate(whisper, "tiny"), { cpuRAM: "2–4", cpuPrecision: "FP32", gpuFloat16RAM: "1–3", gpuFloat32RAM: "2–4", gpuRAM: undefined, gpuPrecision: undefined, gpuSupported: undefined, notes: "Unmeasured planning range", gpuNotes: "Unmeasured planning range" });
    assert.equal(modelMemoryEstimate(whisper, "small").gpuFloat32RAM, undefined);
    assert.equal(modelMemoryEstimate(whisper, "medium").cpuRAM, undefined);
    assert.equal(modelMemoryEstimate(whisper, "large-v3").cpuRAM, "10–14");
    const malformed = { ...whisper, metadata: { ...whisper.metadata, variant_memory_estimates: "bad-json" } };
    assert.equal(modelMemoryEstimate(malformed, "tiny").cpuRAM, undefined);
});


test("recommendation rows surface separate WER evidence without borrowing it for raw benchmark sorts", () => {
    const whisper = model({ model_family: "whisper", model_id: "whisper", metadata: {
        benchmark_model: "large-v3", benchmark_ami_wer: "11.02", additional_benchmarks: JSON.stringify([
            { model: "small", metric: "WER", dataset: "AMI-SDM1", value: 43.5, source: "https://example.test/evaluation" },
        ]),
    } });
    const choice = transcriptionModelChoices([whisper]).find((entry) => entry.model === "small")!;
    assert.match(modelChoiceMetrics(choice), /WER 43.50% · AMI-SDM1 · different test/);
    assert.match(modelChoiceMetrics(choice, "meeting"), /no comparable result/);
    assert.match(modelChoiceMetrics(choice, "conversational"), /no comparable result/);
    assert.equal(choice.meetingWER, undefined);
});


test("GPU sort and labels use runtime precision and keep CPU-only models out of fit claims", () => {
    const models = [
        model({ model_id: "a", metadata: { cpu_float32_ram_gb: "3–4", gpu_float16_vram_gb: "6–10", gpu_float32_vram_gb: "10–14" } }),
        model({ model_id: "b", metadata: { cpu_float32_ram_gb: "10–14", gpu_float32_vram_gb: "5–8", gpu_memory_precision: "FP32 (TF32 math enabled)" } }),
        model({ model_id: "cpu", metadata: { cpu_ram_gb: "2–4", supported_devices: "cpu" } }),
    ];
    const choices = transcriptionModelChoices(models);
    assert.equal(sortModelChoices(choices, "gpu_memory")[0].model, "b");
    assert.match(modelChoiceMetrics(choices.find((item) => item.model === "a")!), /CPU FP32 RAM est. 3–4 GB.*GPU FP16\/BF16 VRAM est. 6–10 GB/);
    assert.match(modelChoiceMetrics(choices.find((item) => item.model === "cpu")!), /GPU unsupported/);
    assert.deepEqual(gpuMemoryEstimate(modelMemoryEstimate(models[0]), "int8"), { precision: "int8" });
    assert.equal(gpuMemoryEstimate(modelMemoryEstimate(models[1]), "float16").value, "5–8");
    assert.match(referenceGPUFit("10–14"), /May exceed/);
    assert.match(referenceGPUFit("13–16"), /Above/);
    assert.match(referenceGPUFit("4–8"), /additional headroom/);
    assert.match(referenceGPUFit(undefined), /No GPU fit estimate/);
});

test("speaker and alignment estimates stay checkpoint-specific and preserve unknown values", () => {
    const capability = model({ metadata: { model_id: "speaker-small", variant_memory_estimates: JSON.stringify([
        { model: "speaker-small", cpu_float32_ram_gb: "2–4", gpu_float32_vram_gb: "2–3", gpu_memory_precision: "FP32" },
        { model: "speaker-large", cpu_float32_ram_gb: "4–8", gpu_vram_gb: "3–6", gpu_memory_precision: "FP32 weights + FP16 encoder autocast" },
    ]), alignment_memory_estimates: JSON.stringify({ model: "aligner", device_policy: "same_as_asr", notes: "Depends on language" }) } });
    assert.equal(gpuMemoryEstimate(modelMemoryEstimate(capability, "speaker-large")).value, "3–6");
    assert.equal(gpuMemoryEstimate(modelMemoryEstimate(capability, "speaker-small")).value, "2–3");
    assert.equal(alignmentMemoryEstimate(capability)?.cpuRAM, undefined);
    assert.equal(alignmentMemoryEstimate(capability)?.devicePolicy, "same_as_asr");
});
