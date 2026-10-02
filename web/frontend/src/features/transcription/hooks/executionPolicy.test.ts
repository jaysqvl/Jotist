import assert from "node:assert/strict";
import test from "node:test";
import { DEFAULT_EXECUTION_POLICY, executionPolicyErrors, globalExecutionPolicy, newRunRecoveryParameters, previewCheckpointReuse, selectRecoveryStrength, sharedRecoveryMode } from "./executionPolicy.ts";
import { canResumeExecution, recoveryStageStatusLabel, type ExecutionRecovery } from "./recoveryPolicy.ts";

test("shared recovery stays on the selected device and has valid bounded defaults", () => {
    assert.equal(sharedRecoveryMode(DEFAULT_EXECUTION_POLICY), "standard");
    assert.equal(sharedRecoveryMode({ ...DEFAULT_EXECUTION_POLICY, reduce_batch_size: false }), "standard");
    assert.equal(sharedRecoveryMode({ ...DEFAULT_EXECUTION_POLICY, automatic_recovery: false }), "fixed");
    assert.deepEqual(executionPolicyErrors(DEFAULT_EXECUTION_POLICY), []);
    assert.equal(executionPolicyErrors({ ...DEFAULT_EXECUTION_POLICY, max_retries: 7 }).length, 1);
    assert.equal(executionPolicyErrors({ ...DEFAULT_EXECUTION_POLICY, max_backoff_seconds: 1 }).length, 1);
});

test("per-run checkpoint choice takes precedence over shared defaults", () => {
    const shared = { ...DEFAULT_EXECUTION_POLICY, reuse_checkpoints: false };
    assert.equal(previewCheckpointReuse({ execution_policy_source: "global" }, shared), false);
    assert.equal(previewCheckpointReuse({ execution_policy_source: "global", reuse_checkpoints: true }, shared), true);
    assert.equal(previewCheckpointReuse({}, shared), false, "older unmarked profiles inherit global defaults for new runs");
});

test("global settings fill missing defaults without replacing an explicit off choice", () => {
    assert.equal(globalExecutionPolicy().automatic_recovery, true);
    assert.equal(globalExecutionPolicy(null).reuse_checkpoints, true);
    assert.equal(globalExecutionPolicy({ max_retries: 2 }).automatic_recovery, true);
    assert.equal(globalExecutionPolicy({ max_retries: 2 }).reuse_checkpoints, true);
    const off = globalExecutionPolicy({ automatic_recovery: false, reuse_checkpoints: false });
    assert.equal(off.automatic_recovery, false);
    assert.equal(off.reuse_checkpoints, false);
});

test("new custom run drops an old execution's off choices without mutating its model settings or saved policy", () => {
    const previous = { model_family: "qwen3_asr", model: "Qwen/Qwen3-ASR-1.7B-hf", device: "cuda",
        execution_policy_source: "override" as const, execution_policy: { ...DEFAULT_EXECUTION_POLICY, automatic_recovery: false, reuse_checkpoints: false },
        recovery_mode: "fixed", reuse_checkpoints: false, transcription_context: "synthetic topic", max_new_tokens: 0,
        adaptive_policy: { stages: { recognition: { allow_cpu: false } } } };
    const original = structuredClone(previous);
    const next = newRunRecoveryParameters(previous);
    assert.equal(next.execution_policy_source, "global");
    assert.equal(next.execution_policy, undefined);
    assert.equal(next.recovery_mode, undefined);
    assert.equal(next.reuse_checkpoints, undefined);
    assert.equal(previewCheckpointReuse(next, DEFAULT_EXECUTION_POLICY), true);
    assert.equal(previewCheckpointReuse(next, { ...DEFAULT_EXECUTION_POLICY, reuse_checkpoints: false }), false);
    assert.equal(next.model, previous.model);
    assert.equal(next.device, previous.device);
    assert.equal(next.transcription_context, previous.transcription_context);
    assert.equal(next.max_new_tokens, 0);
    assert.deepEqual(next.adaptive_policy, previous.adaptive_policy);
    assert.deepEqual(previous, original);
});

test("strength changes select a sufficient default budget and preserve a customized limit", () => {
    const strong = selectRecoveryStrength(DEFAULT_EXECUTION_POLICY, "strong");
    assert.equal(strong.max_retries, 5);
    assert.equal(sharedRecoveryMode(strong), "strong");
    const aggressive = selectRecoveryStrength(strong, "aggressive");
    assert.equal(aggressive.max_retries, 6);
    assert.equal(sharedRecoveryMode(aggressive), "aggressive");
    assert.equal(selectRecoveryStrength({ ...strong, max_retries: 1 }, "aggressive").max_retries, 1);
    assert.equal(sharedRecoveryMode({ ...aggressive, automatic_recovery: false }), "fixed");
});

test("resource waits and scheduled backoff are active, with separate readable labels", () => {
    const recovery = { execution_id: "run", status: "waiting_for_resource", resumable: true } as ExecutionRecovery;
    assert.equal(canResumeExecution(recovery, "run"), false);
    assert.equal(recoveryStageStatusLabel({ id: "stage", kind: "recognition", status: "waiting_for_resource", attempts: [] }), "Waiting for compute resources");
    assert.match(recoveryStageStatusLabel({ id: "stage", kind: "recognition", status: "waiting_for_resource", attempts: [{ id: "retry", attempt_number: 2, status: "waiting_for_resource", retry_at: "2026-09-30T13:00:00Z" }] }), /Retry scheduled/);
});
