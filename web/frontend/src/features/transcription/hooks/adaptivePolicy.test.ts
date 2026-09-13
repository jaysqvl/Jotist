import assert from "node:assert/strict";
import test from "node:test";
import { adaptiveLevel, adaptivePolicyErrors, adaptiveStageChoices, alignmentMemoryForConfiguration, declaredAdaptiveStages, qualifiedWindows, stagePolicy, updateStagePolicy, type AdaptiveStageDescriptor } from "./adaptivePolicy.ts";
import type { TranscriptionModelCapability } from "./modelCapabilities.ts";

const descriptor: AdaptiveStageDescriptor = { kind: "recognition", recoverable: true, device_precisions: { cpu: ["float32"], cuda: ["float16", "float32"] }, qualified_batches: [1, 2, 4], window_policy: { unit: "seconds", candidates: [30, 20, 10], minimum_overlap: 2, stitching_version: "overlap-v1" } };
const model = (id: string, family: string, stages: AdaptiveStageDescriptor[] = []): TranscriptionModelCapability => ({ model_id: id, model_family: family, display_name: id, description: "", features: { timestamps: true, word_level: true }, metadata: { adaptive_stages: JSON.stringify(stages) } });

test("all four recovery levels are available without implicitly granting CPU or window changes", () => {
    assert.deepEqual([adaptiveLevel("fixed"), adaptiveLevel("stage_management"), adaptiveLevel("batch_management"), adaptiveLevel("cpu_fallback"), adaptiveLevel("shorter_windows")], [0, 1, 2, 3, 4]);
    const policy = stagePolicy(undefined, "recognition");
    assert.equal(policy.device_locked, true);
    assert.equal(policy.allow_cpu, false);
    assert.equal(policy.cpu_precision, "float32");
    assert.equal(policy.allow_shorter_windows, false);
    assert.deepEqual(policy.window_candidates, []);
});

test("only declared adapter stages enable adaptive actions, including an independently selected diarizer", () => {
    const params = { model_family: "qwen3_asr", model: "qwen", no_align: false, diarize: true, diarize_model: "pyannote", diarization_checkpoint: "community" };
    const asr = model("qwen", "qwen3_asr", [descriptor]);
    const speakers = model("community", "pyannote", [{ ...descriptor, kind: "diarization" }]);
    const stages = adaptiveStageChoices(params, [asr, speakers]);
    assert.equal(stages[0].descriptor?.kind, "recognition");
    assert.equal(stages[1].descriptor, undefined, "timestamps feature does not qualify independent alignment");
    assert.equal(stages[2].descriptor?.kind, "diarization");
    assert.deepEqual(declaredAdaptiveStages({ ...asr, metadata: {} }), []);
    assert.deepEqual(declaredAdaptiveStages({ ...asr, metadata: { adaptive_stages: "invalid json" } }), []);
    assert.equal(adaptiveStageChoices({ ...params, diarize_model: "native", no_align: true }, [asr]).length, 1);
    assert.equal(adaptiveStageChoices({ ...params, model_family: "nvidia_canary", nvidia_timestamps: false, diarize: false }, []).length, 1, "NVIDIA alignment follows its actual timing switch");
});

test("stage locks withdraw CPU permission without changing another stage or the original policy", () => {
    const first = updateStagePolicy(undefined, "recognition", { device_locked: false, allow_cpu: true });
    const second = updateStagePolicy(first, "diarization", { device_locked: false, allow_cpu: true });
    const locked = updateStagePolicy(second, "recognition", { device_locked: true });
    assert.equal(locked.stages?.recognition?.allow_cpu, false);
    assert.equal(locked.stages?.diarization?.allow_cpu, true);
    assert.equal(second.stages?.recognition?.allow_cpu, true);
    assert.equal(locked.learn, false);
});

test("CPU fallback requires an unlocked stage and an explicitly supported precision", () => {
    const choices = [{ kind: "recognition" as const, label: "Recognition", descriptor }];
    let policy = updateStagePolicy(undefined, "recognition", { allow_cpu: true });
    assert.match(adaptivePolicyErrors({ recovery_mode: "cpu_fallback", adaptive_policy: policy }, choices).join(" "), /unlock/);
    policy = updateStagePolicy(policy, "recognition", { device_locked: false, cpu_precision: "float16" });
    assert.match(adaptivePolicyErrors({ recovery_mode: "cpu_fallback", adaptive_policy: policy }, choices).join(" "), /supported/);
    policy = updateStagePolicy(policy, "recognition", { cpu_precision: "float32" });
    assert.deepEqual(adaptivePolicyErrors({ recovery_mode: "cpu_fallback", adaptive_policy: policy }, choices), []);
    assert.ok(adaptivePolicyErrors({ recovery_mode: "cpu_fallback", adaptive_policy: policy }, [{ ...choices[0], descriptor: undefined }]).length);
    assert.deepEqual(adaptivePolicyErrors({ recovery_mode: "batch_management", adaptive_policy: policy }, choices), [], "lower levels do not activate retained CPU opt-ins");
});

test("shorter windows enforce explicit candidates, floor and validated overlap", () => {
    const choices = [{ kind: "recognition" as const, label: "Recognition", descriptor }];
    let policy = updateStagePolicy(undefined, "recognition", { allow_shorter_windows: true });
    assert.ok(adaptivePolicyErrors({ recovery_mode: "shorter_windows", adaptive_policy: policy }, choices).length);
    policy = updateStagePolicy(policy, "recognition", { window_candidates: [20, 10], min_window_seconds: 10, overlap_seconds: 2 });
    assert.deepEqual(adaptivePolicyErrors({ recovery_mode: "shorter_windows", adaptive_policy: policy }, choices), []);
    for (const patch of [{ window_candidates: [15] }, { window_candidates: [30, 20, 10] }, { window_candidates: [20, 20] }, { min_window_seconds: 11 }, { overlap_seconds: 0 }, { overlap_seconds: 5 }, { overlap_seconds: 10 }]) {
        assert.ok(adaptivePolicyErrors({ recovery_mode: "shorter_windows", adaptive_policy: updateStagePolicy(policy, "recognition", patch) }, choices).length);
    }
    assert.deepEqual(qualifiedWindows({ ...descriptor, window_policy: { ...descriptor.window_policy!, stitching_version: "" } }), []);
});

test("frozen stage settings stay visible and intact until explicitly cleared", () => {
    const fixed = { device: "cpu", precision: "float32", batch_size: 1, concurrency: 1, window_seconds: 20, overlap_seconds: 2 };
    const original = updateStagePolicy(undefined, "alignment", { fixed });
    const edited = updateStagePolicy(original, "alignment", { min_batch_size: 2 });
    assert.deepEqual(stagePolicy(edited, "alignment").fixed, fixed);
    const cleared = updateStagePolicy(edited, "alignment", { fixed: undefined });
    assert.equal(stagePolicy(cleared, "alignment").fixed, undefined);
    assert.deepEqual(stagePolicy(original, "alignment").fixed, fixed, "clearing a draft does not mutate the saved profile");
    assert.equal(JSON.stringify(cleared).includes('"fixed"'), false, "removing the override omits it from the submitted policy");
});

test("Canary memory shows its separate FP32 alignment stage and preserves unknown estimates", () => {
    const canary = model("canary", "nvidia_canary", [{ ...descriptor, kind: "alignment", device_precisions: { cpu: ["float32"], cuda: ["float32"] } }]);
    canary.metadata!.alignment_memory_estimates = JSON.stringify({ model: "Canary embedded NeMo CTC aligner", device_policy: "same_as_asr", fixed_precision: "float32", cpu_memory_precision: "FP32", gpu_memory_precision: "FP32", estimate_status: "unknown" });
    const params = { model_family: "nvidia_canary", model: "canary", device: "cuda", compute_type: "float16", nvidia_precision: "float16", no_align: true, nvidia_timestamps: true };
    const initial = alignmentMemoryForConfiguration(params, [canary])!;
    assert.equal(initial.enabled, true, "NVIDIA timestamp control overrides the unrelated Whisper no_align flag");
    assert.equal(initial.device, "cuda");
    assert.equal(initial.precision, "FP32", "CTC never borrows recognition FP16 precision");
    assert.equal(initial.memory.cpuRAM, undefined);
    assert.equal(initial.gpuRAM, undefined, "unknown CTC memory must not borrow ASR estimates");
    assert.equal(initial.cpuFallbackPrecision, undefined);
    assert.equal(alignmentMemoryForConfiguration({ ...params, nvidia_timestamps: false, no_align: false }, [canary])?.enabled, false);
    const auto = alignmentMemoryForConfiguration({ ...params, device: "auto" }, [canary]);
    assert.equal(auto?.device, "auto");
    assert.equal(auto?.precision, "FP32");
    const cpu = alignmentMemoryForConfiguration({ ...params, device: "cpu" }, [canary]);
    assert.equal(cpu?.device, "cpu");
    assert.equal(cpu?.precision, "FP32");

    const policy = updateStagePolicy(undefined, "alignment", { allow_cpu: true, device_locked: false, cpu_precision: "float32" });
    const fallback = alignmentMemoryForConfiguration({ ...params, recovery_mode: "cpu_fallback", adaptive_policy: policy }, [canary]);
    assert.equal(fallback?.device, "cuda", "permitted fallback is not the initial device");
    assert.equal(fallback?.cpuFallbackPrecision, "float32");
    assert.equal(alignmentMemoryForConfiguration({ ...params, recovery_mode: "batch_management", adaptive_policy: policy }, [canary])?.cpuFallbackPrecision, undefined);
    const fixed = { device: "cpu", precision: "float32", batch_size: 1, concurrency: 1, window_seconds: 0, overlap_seconds: 0 };
    const frozen = alignmentMemoryForConfiguration({ ...params, recovery_mode: "fixed", adaptive_policy: updateStagePolicy(policy, "alignment", { fixed, device_locked: true }) }, [canary]);
    assert.equal(frozen?.device, "cpu");
    assert.equal(frozen?.precision, "FP32");
    assert.equal(frozen?.fixed, true);
    assert.equal(frozen?.cpuFallbackPrecision, undefined);
    assert.equal(params.device, "cuda", "display does not mutate recognition configuration");
});
