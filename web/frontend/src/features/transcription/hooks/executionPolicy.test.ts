import assert from "node:assert/strict";
import test from "node:test";
import { DEFAULT_EXECUTION_POLICY, executionPolicyErrors, previewCheckpointReuse, sharedRecoveryMode } from "./executionPolicy.ts";
import { canResumeExecution, recoveryStageStatusLabel, type ExecutionRecovery } from "./recoveryPolicy.ts";

test("shared recovery stays on the selected device and has valid bounded defaults", () => {
    assert.equal(sharedRecoveryMode(DEFAULT_EXECUTION_POLICY), "batch_management");
    assert.equal(sharedRecoveryMode({ ...DEFAULT_EXECUTION_POLICY, reduce_batch_size: false }), "stage_management");
    assert.equal(sharedRecoveryMode({ ...DEFAULT_EXECUTION_POLICY, automatic_recovery: false }), "fixed");
    assert.deepEqual(executionPolicyErrors(DEFAULT_EXECUTION_POLICY), []);
    assert.equal(executionPolicyErrors({ ...DEFAULT_EXECUTION_POLICY, max_retries: 7 }).length, 1);
    assert.equal(executionPolicyErrors({ ...DEFAULT_EXECUTION_POLICY, max_backoff_seconds: 1 }).length, 1);
});

test("per-run checkpoint choice takes precedence over shared defaults", () => {
    const shared = { ...DEFAULT_EXECUTION_POLICY, reuse_checkpoints: false };
    assert.equal(previewCheckpointReuse({ execution_policy_source: "global" }, shared), false);
    assert.equal(previewCheckpointReuse({ execution_policy_source: "global", reuse_checkpoints: true }, shared), true);
    assert.equal(previewCheckpointReuse({}, shared), true, "legacy nil retains exact-compatible reuse");
});

test("resource waits and scheduled backoff are active, with separate readable labels", () => {
    const recovery = { execution_id: "run", status: "waiting_for_resource", resumable: true } as ExecutionRecovery;
    assert.equal(canResumeExecution(recovery, "run"), false);
    assert.equal(recoveryStageStatusLabel({ id: "stage", kind: "recognition", status: "waiting_for_resource", attempts: [] }), "Waiting for compute resources");
    assert.match(recoveryStageStatusLabel({ id: "stage", kind: "recognition", status: "waiting_for_resource", attempts: [{ id: "retry", attempt_number: 2, status: "waiting_for_resource", retry_at: "2026-09-30T13:00:00Z" }] }), /Retry scheduled/);
});
