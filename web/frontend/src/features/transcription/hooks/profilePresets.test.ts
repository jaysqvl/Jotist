import assert from "node:assert/strict";
import test from "node:test";
import { TRANSCRIPTION_PRESETS, RECOMMENDED_PRESETS, createPresetDraft, presetAlreadyAdded } from "./profilePresets.ts";
import { REFERENCE_PROFILE_VALUES } from "./referenceProfilePresets.ts";

test("preset catalog preserves all nine captured configurations, including the GPU-named CPU preset", () => {
    assert.equal(TRANSCRIPTION_PRESETS.length, 12);
    assert.equal(new Set(TRANSCRIPTION_PRESETS.map((preset) => preset.id)).size, 12);
    assert.equal(REFERENCE_PROFILE_VALUES.length, 9);
    for (const { name, ...parameters } of REFERENCE_PROFILE_VALUES) {
        const preset = TRANSCRIPTION_PRESETS.find((preset) => preset.name === name)!;
        assert.ok(preset, name);
        assert.equal(preset.origin, "existing");
        for (const [key, value] of Object.entries(parameters)) assert.deepEqual(preset.parameters[key], value, `${name}: ${key}`);
    }
    const mismatch = TRANSCRIPTION_PRESETS.find((preset) => preset.name === "GPU-PARAKEET-PYANNOTE")!;
    assert.equal(mismatch.parameters.device, "cpu");
    assert.equal(mismatch.parameters.batch_size, 8);
    assert.equal(mismatch.parameters.attention_context_left, 512);
    assert.equal(mismatch.parameters.diarization_device, "auto");
    assert.match(mismatch.notes, /Name\/device mismatch/);
    const canary = TRANSCRIPTION_PRESETS.find((preset) => preset.name === "CPU-CANARY-PYANNOTE")!;
    assert.equal(canary.parameters.nvidia_precision, "bfloat16");
    assert.equal(canary.parameters.batch_size, 2);
    const noSpeakers = TRANSCRIPTION_PRESETS.find((preset) => preset.name === "GPU-CANARY-NOSPEAKER")!;
    assert.equal(noSpeakers.parameters.diarize, false);
    assert.equal(noSpeakers.parameters.nvidia_timestamps, false);
});

test("recommended presets use explicit CPU/GPU devices and batch size 1 independently of captured profiles", () => {
    const hybrid = TRANSCRIPTION_PRESETS.find((preset) => preset.id === "hybrid-canary-pyannote")!;
    assert.equal(hybrid.parameters.device, "cuda");
    assert.equal(hybrid.parameters.diarization_device, "cpu");
    assert.equal(hybrid.parameters.diarize, true);
    for (const preset of RECOMMENDED_PRESETS) {
        const params = preset.parameters;
        assert.equal(params.batch_size, 1);
        assert.equal(params.compute_type, params.device === "cpu" ? "float32" : "float16");
        assert.equal(params.nvidia_precision, params.compute_type);
        assert.equal(params.fp16, params.device === "cuda");
        assert.ok(["cpu", "cuda"].includes(params.diarization_device));
    }
    const initial = RECOMMENDED_PRESETS.find((preset) => preset.id === "cpu-qwen-pyannote")!;
    assert.equal(initial.parameters.model, "Qwen/Qwen3-ASR-1.7B-hf");
    assert.equal(initial.parameters.device, "cpu");
    assert.equal(initial.parameters.diarization_device, "cpu");
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
    assert.equal(draft.parameters.language, null, "editing a reference must retain its nullable fields until explicitly changed");
    draft.parameters.model = "edited-in-dialog";
    assert.equal(preset.parameters.model, originalModel);
    assert.equal(createPresetDraft(preset, []).parameters.model, originalModel);
    assert.equal(presetAlreadyAdded(preset, [preset.name.toUpperCase()]), true);
    assert.equal(presetAlreadyAdded(preset, ["another profile"]), false);
});
