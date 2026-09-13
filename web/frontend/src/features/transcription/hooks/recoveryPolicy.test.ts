import assert from "node:assert/strict";
import test from "node:test";
import { canResumeExecution, devicePolicyDescription, partialRecoveryDownload, partialRecoveryText, recoveryAttemptErrorLabel, recoveryAttemptReasonLabel, recoveryMemoryRows, recoveryModeLabel, shouldReuseCheckpoints, stageLabel, usesLegacyAuto, type ExecutionRecovery, type RecoveryAttempt } from "./recoveryPolicy.ts";
import { buildImmediateRunRequest, buildQueueRequest } from "./transcriptionQueue.ts";
import { RECOMMENDED_PRESETS, TRANSCRIPTION_PRESETS } from "./profilePresets.ts";

test("legacy Auto includes independently automatic speaker devices without relabeling fixed or Level 1", () => {
    assert.equal(recoveryModeLabel({ model_family: "whisper", device: "auto" }), "Legacy Auto fallback");
    assert.equal(usesLegacyAuto({ model_family: "nvidia_canary", device: "cpu", diarize: true, diarize_model: "pyannote" }), true);
    assert.equal(usesLegacyAuto({ model_family: "moss_asr", device: "cpu", diarize: true, diarize_model: "native", diarization_device: "auto" }), false);
    assert.equal(recoveryModeLabel({ recovery_mode: "fixed", device: "auto" }), "Fixed settings");
    assert.equal(recoveryModeLabel({ recovery_mode: "stage_management", device: "auto" }), "Level 1 · Stage management");
    assert.match(devicePolicyDescription("fixed"), /does not retry.*CPU/);
    assert.match(devicePolicyDescription("stage_management"), /does not retry.*CPU/);
    assert.match(devicePolicyDescription(""), /Legacy Auto.*retry/);
});

test("new recommended profiles use fixed execution while captured references retain legacy semantics", () => {
    assert.ok(RECOMMENDED_PRESETS.every((preset) => preset.parameters.recovery_mode === "fixed" && preset.parameters.reuse_checkpoints === true));
    assert.ok(TRANSCRIPTION_PRESETS.filter((preset) => preset.origin === "existing").every((preset) => preset.parameters.recovery_mode === undefined));
});

test("checkpoint reuse defaults on but an explicit fresh request survives all submission shapes", () => {
    assert.equal(shouldReuseCheckpoints(undefined), true);
    assert.equal(shouldReuseCheckpoints(null), true);
    assert.equal(shouldReuseCheckpoints(false), false);
    const parameters = { model_family: "qwen3_asr", hf_token: "synthetic-custom", recovery_mode: "fixed" };
    assert.deepEqual(buildImmediateRunRequest(parameters, "saved-profile", { reuse_checkpoints: false }), { profile_id: "saved-profile", reuse_checkpoints: false });
    assert.deepEqual(buildImmediateRunRequest(parameters, "saved-profile"), { profile_id: "saved-profile" });
    assert.deepEqual(buildImmediateRunRequest(parameters, undefined, { reuse_checkpoints: false }), { ...parameters, reuse_checkpoints: false });
    assert.deepEqual(buildQueueRequest({ parameters, profile_id: "saved-profile", reuse_checkpoints: false }), { parameters, profile_id: "saved-profile", reuse_checkpoints: false });
    assert.equal(Object.hasOwn(buildQueueRequest({ parameters, profile_id: "saved-profile" }), "reuse_checkpoints"), false);
    assert.equal(Object.hasOwn(parameters, "reuse_checkpoints"), false);
});

const recovery = (changes: Partial<ExecutionRecovery> = {}): ExecutionRecovery => ({
    execution_id: "execution-a", mode: "fixed", status: "interrupted", resumable: true,
    partial_transcript_available: false, stages: [], ...changes,
});

test("resume requires authoritative server permission for the exact selected execution and no active competitor", () => {
    assert.equal(canResumeExecution(undefined, "execution-a"), false);
    assert.equal(canResumeExecution(recovery(), "execution-b"), false);
    assert.equal(canResumeExecution(recovery({ resumable: false }), "execution-a"), false);
    assert.equal(canResumeExecution(recovery({ status: "running" }), "execution-a"), false);
    assert.equal(canResumeExecution(recovery(), "execution-a", true), false);
    assert.equal(canResumeExecution(recovery(), "execution-a"), true);
});

test("partial text requires an explicit retained-output flag and remains distinct from completion", () => {
    assert.equal(partialRecoveryText(recovery({ partial_transcript: { text: "not declared retained" } })), undefined);
    assert.equal(partialRecoveryText(recovery({ partial_transcript_available: true })), undefined);
    assert.equal(partialRecoveryText(recovery({ partial_transcript_available: true, partial_transcript: { text: "Technical meeting text" } })), "Technical meeting text");
    assert.equal(partialRecoveryText(recovery({ partial_transcript_available: true, partial_transcript: { text: "" } })), "");
    assert.equal(stageLabel({ id: "combined", kind: "combined", status: "failed", recoverable_boundary: false, attempts: [] }), "Combined model output");
});

test("partial downloads export exact retained text only for the selected execution", async () => {
    const text = "C++ parseHTTP(\"a&b\"); <script>plain text</script>\n日本語\n";
    const saved = recovery({ partial_transcript_available: true, partial_transcript: { text } });
    assert.equal(partialRecoveryDownload(saved, "different-execution"), undefined);
    assert.equal(partialRecoveryDownload(recovery({ partial_transcript: { text } }), "execution-a"), undefined);
    const download = partialRecoveryDownload(saved, "execution-a");
    assert.ok(download);
    assert.equal(download.filename, "partial-transcript-execution-a.txt");
    assert.equal(download.blob.type, "text/plain;charset=utf-8");
    assert.equal(await download.blob.text(), text);
    assert.equal(saved.partial_transcript?.text, text);
    const unusualID = "../../run/name";
    const safe = partialRecoveryDownload({ ...saved, execution_id: unusualID }, unusualID);
    assert.ok(safe);
    assert.match(safe.filename, /^partial-transcript-[a-zA-Z0-9_-]+\.txt$/);
});

test("attempt labels explain recovery without claiming unsupported policy changes", () => {
    assert.match(recoveryAttemptReasonLabel("cleanup_retry"), /One GPU retry.*same settings/);
    assert.equal(recoveryAttemptReasonLabel("legacy_cpu_fallback"), "Legacy Auto retry on CPU with FP32");
    assert.equal(recoveryAttemptReasonLabel("resume_same_settings"), "Resumed with the saved settings");
    assert.match(recoveryAttemptErrorLabel("checkpoint_persistence_failed"), /could not be saved/);
    assert.match(recoveryAttemptErrorLabel("worker_interrupted"), /before this stage completed/);
    assert.match(recoveryAttemptErrorLabel("cuda_out_of_memory"), /ran out of memory/);
    assert.equal(recoveryAttemptReasonLabel("unknown_policy"), "Additional attempt recorded");
    assert.equal(recoveryAttemptErrorLabel("unknown_failure"), "This attempt stopped with an unrecognized error code.");
});

test("attempt telemetry keeps host RSS, device VRAM and allocator peaks distinct without inventing missing samples", () => {
    const base: RecoveryAttempt = { id: "synthetic-attempt", attempt_number: 1, status: "failed", device: "cpu" };
    assert.deepEqual(recoveryMemoryRows(base), []);
    const m = { samples: 2, elapsed_seconds: 1, scope: "synthetic-fixture", external_contention: false };
    const cpu = recoveryMemoryRows({ ...base, measurements: { ...m, host_total_bytes: 32, host_available_before_bytes: 20, host_minimum_available_bytes: 10 } });
    assert.equal(cpu.find((row) => row.label === "Owned process peak RSS")?.bytes, undefined);
    assert.equal(cpu.find((row) => row.label === "Host or container memory capacity")?.bytes, 32);
    assert.equal(cpu.find((row) => row.label === "Minimum host memory available")?.bytes, 10);
    assert.equal(cpu.some((row) => row.label.includes("GPU") || row.label.includes("VRAM")), false);
    const gpu = recoveryMemoryRows({ ...base, device: "cuda", measurements: { ...m, process_peak_bytes: 8, device_peak_used_bytes: 12, torch_peak_allocated_bytes: 0, torch_peak_reserved_bytes: 6 } });
    assert.equal(gpu.find((row) => row.label === "Owned process peak VRAM")?.bytes, 8);
    assert.equal(gpu.find((row) => row.label === "Device-wide peak")?.bytes, 12);
    assert.equal(gpu.find((row) => row.label === "PyTorch peak allocated")?.bytes, 0);
    assert.equal(gpu.find((row) => row.label === "PyTorch peak reserved")?.bytes, 6);
    assert.equal(gpu.find((row) => row.label === "GPU capacity")?.bytes, undefined);
    assert.equal(gpu.some((row) => row.label.includes("RSS")), false);
});
