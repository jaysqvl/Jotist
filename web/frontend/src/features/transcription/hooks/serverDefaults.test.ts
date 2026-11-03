import assert from "node:assert/strict";
import test from "node:test";
import { applyServerDraftDefaults, serverDefaultDevice } from "./serverDefaults.ts";

test("installation defaults come from the server catalog and preserve old responses", () => {
    assert.equal(serverDefaultDevice({ execution_defaults: { architecture: "arm64", default_device: "cpu" } }), "cpu");
    assert.equal(serverDefaultDevice({ execution_defaults: { architecture: "amd64", default_device: "cuda" } }), "cuda");
    assert.equal(serverDefaultDevice({ models: {} }), "cuda");
    assert.equal(serverDefaultDevice(null), "cuda");
});

test("ARM changes only a new draft's resource defaults", () => {
    const original = { model_family: "whisper", model: "small", device: "cuda", compute_type: "float16", fp16: true, batch_size: 1, task: "transcribe", diarize: true, diarize_model: "diarizen", language: "fr", context: "private synthetic context", reuse_checkpoints: false };
    const cpu = applyServerDraftDefaults(original, "cpu", [], false, false);
    assert.equal(cpu.device, "cpu");
    assert.equal(cpu.compute_type, "float32");
    assert.equal(cpu.fp16, false);
    assert.equal(cpu.context, original.context);
    assert.equal(cpu.language, original.language);
    assert.equal(cpu.reuse_checkpoints, false);
    assert.equal(original.device, "cuda");
    assert.equal(applyServerDraftDefaults(original, "cuda", [], false, false), original);
    assert.equal(applyServerDraftDefaults(original, "cpu", [], true, false), original, "saved profiles and previous-run parameters remain exact");
    assert.equal(applyServerDraftDefaults(original, "cpu", [], false, true), original, "a late response cannot overwrite an explicit device choice");
});
