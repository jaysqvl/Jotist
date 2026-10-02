import assert from "node:assert/strict";
import test from "node:test";
import { TRANSCRIPTION_PRESETS, RECOMMENDED_PRESETS, ALTERNATIVE_PRESETS, createPresetDraft, presetAlreadyAdded } from "./profilePresets.ts";
import { DEFAULT_EXECUTION_POLICY, previewCheckpointReuse } from "./executionPolicy.ts";

test("public starters have unique identities and accurate device labels without captured user configurations", () => {
    assert.equal(TRANSCRIPTION_PRESETS.length, 12);
    assert.equal(new Set(TRANSCRIPTION_PRESETS.map((preset) => preset.id)).size, TRANSCRIPTION_PRESETS.length);
    assert.equal(new Set(TRANSCRIPTION_PRESETS.map((preset) => preset.name)).size, TRANSCRIPTION_PRESETS.length);
    assert.equal(TRANSCRIPTION_PRESETS.some((preset) => preset.id.startsWith("reference-")), false);
    for (const preset of TRANSCRIPTION_PRESETS) {
        assert.ok(preset.name.startsWith(preset.group));
        assert.equal(preset.parameters.execution_policy_source, "global");
        assert.equal(preset.parameters.execution_policy, undefined);
        assert.equal(preset.parameters.recovery_mode, undefined);
        assert.equal(preset.parameters.reuse_checkpoints, undefined);
        assert.equal(previewCheckpointReuse(preset.parameters, DEFAULT_EXECUTION_POLICY), true);
        assert.equal(previewCheckpointReuse(preset.parameters, { ...DEFAULT_EXECUTION_POLICY, reuse_checkpoints: false }), false);
    }
});

test("GPU starters follow the transcription device while alternatives retain explicit CPU and hybrid choices", () => {
    const hybrid = TRANSCRIPTION_PRESETS.find((preset) => preset.id === "hybrid-parakeet-pyannote")!;
    assert.equal(hybrid.parameters.device, "cuda");
    assert.equal(hybrid.parameters.diarization_device, "cpu");
    assert.equal(hybrid.parameters.diarize, true);
    assert.equal(hybrid.parameters.compute_type, "float32", "Parakeet's runtime keeps FP32 weights on GPU");
    assert.equal(hybrid.parameters.fp16, false);
    for (const preset of TRANSCRIPTION_PRESETS) {
        const params = preset.parameters;
        assert.equal(params.batch_size, 1);
        assert.equal(params.compute_type, params.device === "cpu" || params.model_family === "nvidia_parakeet" ? "float32" : "float16");
        assert.equal(params.nvidia_precision, params.compute_type);
        assert.equal(params.fp16, params.compute_type === "float16");
        assert.equal(params.language, "en");
        assert.ok(["cpu", "cuda", "same"].includes(params.diarization_device));
    }
    for (const preset of RECOMMENDED_PRESETS) {
        assert.equal(preset.parameters.device, "cuda");
        assert.equal(preset.parameters.diarization_device, "same");
        assert.equal(preset.group, "GPU");
    }
    const initial = ALTERNATIVE_PRESETS.find((preset) => preset.id === "cpu-qwen-pyannote")!;
    assert.equal(initial.parameters.model, "Qwen/Qwen3-ASR-1.7B-hf");
    assert.equal(initial.parameters.device, "cpu");
    assert.equal(initial.parameters.diarization_device, "cpu");
    const gpuDefault = RECOMMENDED_PRESETS.find((preset) => preset.id === "gpu-qwen-pyannote")!;
    assert.equal(gpuDefault.parameters.device, "cuda");
    assert.equal(gpuDefault.parameters.diarization_device, "same");
    assert.equal(gpuDefault.parameters.execution_policy_source, "global");
    const canary = RECOMMENDED_PRESETS.find((preset) => preset.id === "gpu-canary-pyannote")!;
    assert.equal(canary.parameters.model, "canary");
    assert.equal(canary.parameters.nvidia_chunk_duration, 40);
    assert.equal(canary.parameters.nvidia_use_chunking, true);
    const sortformer = RECOMMENDED_PRESETS.find((preset) => preset.id === "gpu-parakeet-sortformer")!;
    assert.equal(sortformer.parameters.diarize_model, "nvidia_sortformer");
    assert.equal(sortformer.parameters.max_speakers, 4);
    const diarizen = RECOMMENDED_PRESETS.find((preset) => preset.id === "gpu-qwen-diarizen")!;
    assert.equal(diarizen.parameters.diarize_model, "diarizen");
    assert.equal(diarizen.parameters.diarization_checkpoint, "BUT-FIT/diarizen-wavlm-large-s80-md-v2");
    for (const id of ["cpu-qwen-small-pyannote", "cpu-granite-compact-pyannote"]) {
        const compact = ALTERNATIVE_PRESETS.find((preset) => preset.id === id)!;
        assert.equal(compact.parameters.device, "cpu");
        assert.equal(compact.parameters.diarization_device, "cpu");
        assert.equal(compact.parameters.diarization_checkpoint, "pyannote/speaker-diarization-community-1");
    }
});

test("every preset inherits credentials and context without embedded secrets or private fields", () => {
    for (const preset of TRANSCRIPTION_PRESETS) {
        const params = preset.parameters;
        assert.equal(params.hf_token_source, "default");
        assert.equal(params.transcription_context, null);
        assert.equal(params.transcription_context_terms, null);
        for (const privateField of ["hf_token", "api_key", "initial_prompt", "nvidia_prompt", "callback_url", "model_dir", "align_model"]) assert.equal(privateField in params, false);
        assert.equal(params.is_multi_track_enabled, false);
    }
});

test("loading a preset creates an independent review draft with a non-conflicting name", () => {
    const preset = TRANSCRIPTION_PRESETS[0];
    const originalModel = preset.parameters.model;
    const draft = createPresetDraft(preset, [preset.name.toUpperCase(), `${preset.name} (2)`]);
    assert.equal(draft.name, `${preset.name} (3)`);
    assert.equal("id" in draft, false, "drafts must create new profiles rather than update an existing ID");
    assert.equal(draft.parameters.language, "en");
    assert.equal(draft.parameters.execution_policy_source, "global");
    draft.parameters.model = "edited-in-dialog";
    assert.equal(preset.parameters.model, originalModel);
    assert.equal(createPresetDraft(preset, []).parameters.model, originalModel);
    assert.equal(presetAlreadyAdded(preset, [preset.name.toUpperCase()]), true);
    assert.equal(presetAlreadyAdded(preset, ["another profile"]), false);
});
