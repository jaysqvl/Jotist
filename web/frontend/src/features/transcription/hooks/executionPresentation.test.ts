import assert from "node:assert/strict";
import test from "node:test";
import { executionModelSummary, executionEvidenceRows, reportedExecutionPrecision, reportedExecutionTiming, requestedExecutionPrecision, requestedExecutionTiming } from "./executionPresentation.ts";

test("runtime precision stays separate from requests and Parakeet never borrows a saved half-precision setting", () => {
    const parakeet = { model_family: "nvidia_parakeet", nvidia_precision: "bfloat16", compute_type: "float16", device: "auto" };
    assert.equal(requestedExecutionPrecision(parakeet), "FP32 (fixed runtime)");
    assert.equal(reportedExecutionPrecision(parakeet), "Not recorded");
    assert.equal(reportedExecutionPrecision(parakeet, { precision: "float32", resolved_device: "cpu" }), "FP32");
    assert.equal(reportedExecutionPrecision(parakeet, { precision: "float32", resolved_device: "cuda" }), "FP32 (TF32 math enabled)");
    const canary = { model_family: "nvidia_canary", nvidia_precision: "float16", compute_type: "int8" };
    assert.equal(requestedExecutionPrecision(canary), "FP16");
    assert.equal(reportedExecutionPrecision(canary, { precision: "float32", resolved_device: "cpu" }), "FP32");
    assert.equal(requestedExecutionPrecision({ model_family: "test", model: "future", compute_type: "float32" }, { model_id: "future", model_family: "test", display_name: "Future fixed runtime", description: "", features: {}, metadata: { fixed_precision: "bfloat16" } }), "BF16 (fixed runtime)");
});

test("requested timing follows alignment or NVIDIA parameters without hiding reported native timestamps", () => {
    assert.equal(requestedExecutionTiming({ model_family: "qwen3_asr", no_align: true, nvidia_timestamps: true }), "Word alignment disabled");
    assert.equal(requestedExecutionTiming({ model_family: "qwen3_asr", no_align: false, nvidia_timestamps: false }), "Word alignment enabled");
    assert.equal(requestedExecutionTiming({ model_family: "whisper", no_align: true }), "Segment timing; word alignment disabled");
    assert.equal(requestedExecutionTiming({ model_family: "nvidia_canary", nvidia_timestamps: false, no_align: false }), "Disabled");
    assert.equal(requestedExecutionTiming({ model_family: "moss_asr", model: "OpenMOSS-Team/MOSS-Transcribe-Diarize", no_align: true }), "Native timestamps");
    assert.equal(reportedExecutionTiming({ timestamp_source: "native_segments" }), "Native segment timestamps");
    assert.equal(reportedExecutionTiming({ timestamp_source: "native_word_ends_previous_end_starts" }), "Native word ends; inferred starts");
    assert.equal(reportedExecutionTiming({ timestamp_source: "none" }), "No timestamps reported");
    assert.equal(reportedExecutionTiming(), "Not recorded");
});

test("execution details identify the saved recovery policy and never present requested timing as measured", () => {
    const rows = executionEvidenceRows({ model_family: "qwen3_asr", recovery_mode: "stage_management", compute_type: "float16", no_align: false });
    assert.equal(rows.find((row) => row.label === "Recovery policy")?.value, "Level 1 · Stage management");
    assert.equal(rows.find((row) => row.label === "Requested precision")?.value, "FP16");
    assert.equal(rows.find((row) => row.label === "Reported precision")?.value, "Not recorded");
    assert.equal(rows.find((row) => row.label === "Requested timing")?.value, "Word alignment enabled");
    assert.equal(rows.find((row) => row.label === "Reported timing")?.value, "Not recorded");
});

test("run summaries identify exact ASR and speaker checkpoints from output, including CPU fallback", () => {
    const rows = executionModelSummary({ model_family: "cohere_transcribe", model: "CohereLabs/cohere-transcribe-03-2026", device: "cuda", compute_type: "float16", diarize: true, diarize_model: "diarizen", diarization_device: "cuda" }, {
        model_used: "CohereLabs/cohere-transcribe-03-2026",
        metadata: { resolved_device: "cpu", precision: "float32", diarization_model: "BUT-FIT/diarizen-wavlm-large-s80-md-v2", diarization_resolved_device: "cpu", diarization_device: "cuda", diarization_precision: "float32" },
    });
    assert.equal(rows[0].model, "Cohere Transcribe 2B · March 2026");
    assert.equal(rows[0].runtime, "CPU · FP32");
    assert.equal(rows[1].model, "DiariZen WavLM Large v2");
    assert.equal(rows[1].checkpoint, "BUT-FIT/diarizen-wavlm-large-s80-md-v2");
    assert.equal(rows[1].runtime, "CPU · FP32");
    assert.ok(rows.every((row) => row.recorded));
});

test("legacy adapter IDs and saved choices never imply current checkpoint versions or actual devices", () => {
    const rows = executionModelSummary({ model_family: "nvidia_canary", model: "small", device: "cpu", nvidia_precision: "bfloat16", diarize: true, diarize_model: "pyannote" }, { metadata: { model_id: "canary" } });
    assert.equal(rows[0].model, "NVIDIA Canary");
    assert.equal(rows[0].recorded, false);
    assert.equal(rows[0].runtime, "CPU requested · BF16 requested");
    assert.equal(rows[1].model, "Pyannote");
    assert.equal(rows[1].checkpoint, "pyannote");
    assert.equal(rows[1].recorded, false);
    assert.equal(rows[1].runtime, "Auto requested · Precision not recorded");
    const canary = executionModelSummary({ model_family: "nvidia_canary", model: "small" }, { model_used: "canary-1b-v2" });
    assert.equal(canary[0].model, "NVIDIA Canary 1B v2");
    assert.equal(canary[0].recorded, true);
});

test("native speakers share their recorded model runtime, while disabled diarization stays off", () => {
    const params = { model_family: "moss_asr", model: "OpenMOSS-Team/MOSS-Transcribe-Diarize", diarize: true, diarize_model: "native", device: "auto" };
    const rows = executionModelSummary(params, { model_used: params.model, metadata: { resolved_device: "cuda", precision: "float16" } });
    assert.equal(rows[1].model, "MOSS Transcribe Diarize 0.9B · native");
    assert.equal(rows[1].runtime, "GPU · FP16");
    assert.equal(rows[1].recorded, true);
    const recordedNative = executionModelSummary(params, { model_used: params.model, metadata: { resolved_device: "cuda", precision: "float16", diarization_model: params.model, diarization_device: "cuda" } });
    assert.equal(recordedNative[1].model, rows[1].model);
    assert.equal(recordedNative[1].runtime, "GPU · FP16");
    assert.equal(executionModelSummary({ ...params, diarize: false })[1].disabled, true);
});

test("Granite, Qwen, and speaker variants retain distinguishing sizes and versions", () => {
    const choices = ["ibm-granite/granite-speech-4.1-2b", "ibm-granite/granite-speech-4.1-2b-plus", "ibm-granite/granite-speech-5.0-470m-turboctc", "ibm-granite/granite-speech-5.0-470m-turboctc-nc", "Qwen/Qwen3-ASR-0.6B-hf", "Qwen/Qwen3-ASR-1.7B-hf"];
    assert.equal(new Set(choices.map((model) => executionModelSummary({ model })[0].model)).size, choices.length);
    for (const model of ["pyannote/speaker-diarization-community-1", "pyannote/speaker-diarization-3.1", "rewayai/suplime", "rewayai/suplime-large"]) {
        const row = executionModelSummary({ diarize: true, diarize_model: "pyannote", diarization_checkpoint: "pyannote/speaker-diarization-3.1" }, { metadata: { diarization_model: model } })[1];
        assert.equal(row.checkpoint, model);
        assert.equal(row.recorded, true);
    }
});
