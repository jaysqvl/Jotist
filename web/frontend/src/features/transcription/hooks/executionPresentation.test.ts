import assert from "node:assert/strict";
import test from "node:test";
import { executionEvidenceRows, reportedExecutionPrecision, reportedExecutionTiming, requestedExecutionPrecision, requestedExecutionTiming } from "./executionPresentation.ts";

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
