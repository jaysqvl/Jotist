import assert from "node:assert/strict";
import test from "node:test";
import { buildLearningAction, learningPlanIsCurrent, measuredDuration, measuredMemory, type AdaptiveLearningResponse, type AdaptiveLearnedPlan } from "./adaptiveLearning.ts";

const plan: AdaptiveLearnedPlan = { id: "plan-one", profile_revision: 2, learning_generation: 3, scope_key: "scope", scope: { stage_key: "alignment", workload_class: "short", memory_domain: "cuda" }, settings: { device: "cuda", precision: "float32", batch_size: 2, concurrency: 1, window_seconds: 30, overlap_seconds: 2 }, observation_ids: ["observed-1", "observed-2", "observed-3"], created_at: "2026-09-12" };
const data: AdaptiveLearningResponse = { profile_id: "profile", profile_revision: 2, learning_generation: 3, status: "verified", selected_plan_ids: [plan.id], plans: [plan], observations: [], revisions: [{ revision: 1, saved_at: "2026-09-11", reason: "created", name: "Original", parameters: {} }] };

test("profile learning actions carry exact revision and generation guards and never fabricate a freeze target", () => {
    assert.deepEqual(buildLearningAction(data, "reset"), { path: "/api/v1/profiles/profile/reset-adaptive", body: { expected_revision: 2, expected_generation: 3 } });
    assert.deepEqual(buildLearningAction(data, "freeze", plan.id), { path: "/api/v1/profiles/profile/freeze-adaptive", body: { expected_revision: 2, expected_generation: 3, plan_id: "plan-one" } });
    assert.deepEqual(buildLearningAction(data, "restore", 1), { path: "/api/v1/profiles/profile/restore-revision", body: { expected_revision: 2, expected_generation: 3, revision: 1 } });
    assert.throws(() => buildLearningAction({ ...data, plans: [] }, "freeze", plan.id), /measured plan/);
    assert.throws(() => buildLearningAction({ ...data, profile_revision: 0 }, "reset"), /Reload/);
    assert.throws(() => buildLearningAction(data, "restore", 99), /previous saved/);
});

test("current selection authority preserves older source evidence while rejecting unselected or unsupported plans", () => {
    assert.equal(learningPlanIsCurrent(data, plan), true);
    assert.equal(learningPlanIsCurrent(data, { ...plan, profile_revision: 1 }), true);
    assert.equal(learningPlanIsCurrent(data, { ...plan, learning_generation: 1 }), true);
    assert.equal(learningPlanIsCurrent(data, { ...plan, observation_ids: [] }), false);
    assert.equal(learningPlanIsCurrent(data, { ...plan, observation_ids: ["one", "two"] }), false);
    assert.equal(learningPlanIsCurrent({ ...data, selected_plan_ids: [] }, plan), false, "only server-selected qualified plans can be frozen");
    assert.deepEqual(buildLearningAction({ ...data, profile_revision: 3, learning_generation: 4 }, "freeze", plan.id).body, { expected_revision: 3, expected_generation: 4, plan_id: plan.id }, "actions guard current state, not the source evidence generation");
    assert.throws(() => buildLearningAction({ ...data, selected_plan_ids: [] }, "freeze", plan.id), /measured plan/);
});

test("missing telemetry remains unknown rather than zero memory or instantaneous processing", () => {
    assert.equal(measuredMemory(undefined), "Not measured");
    assert.equal(measuredMemory(null), "Not measured");
    assert.equal(measuredMemory(NaN), "Not measured");
    assert.equal(measuredMemory(0), "0.0 MiB");
    assert.equal(measuredMemory(2 * 1024 ** 3), "2.00 GiB");
    assert.equal(measuredDuration(undefined), "Not measured");
    assert.equal(measuredDuration(1500), "1.5s");
});
