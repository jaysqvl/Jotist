import assert from "node:assert/strict";
import test from "node:test";
import type { WhisperXParams } from "../types.ts";
import type { TranscriptionModelCapability } from "./modelCapabilities.ts";
import {
    buildProfileChoices, DEFAULT_PROFILE_PICKER_PREFERENCES, filterAndSortProfiles,
    parseProfilePickerPreferences, profileLanguageOptions, profilePickerStorageKey,
    readProfilePickerPreferences, restoreProfileSelection, saveProfilePickerPreferences,
    type ProfilePickerPreferences, type SavedTranscriptionProfile,
} from "./profilePicker.ts";

const capability = (id: string, metadata: Record<string, string> = {}): TranscriptionModelCapability => ({
    model_id: id, model_family: "example_asr", display_name: id, description: "", features: {},
    supported_languages: ["en", "fr", "auto"], metadata,
});
const profile = (id: string, model: string, changes: Partial<WhisperXParams> = {}): SavedTranscriptionProfile => ({
    id, name: id, is_default: false, created_at: "2026-09-30T00:00:00Z", updated_at: "2026-09-30T00:00:00Z",
    parameters: { model_family: "example_asr", model, language: "en", device: "cuda", compute_type: "float16", diarize: true, diarize_model: "pyannote", ...changes } as WhisperXParams,
});
const preferences = (changes: Partial<ProfilePickerPreferences> = {}) => ({ ...DEFAULT_PROFILE_PICKER_PREFERENCES, ...changes });

test("WER sorting compares exact checkpoints, leaves unknown results last, and never changes saved parameters", () => {
    const models = [capability("org/a", { benchmark_ami_wer: "8.31", benchmark_conversational_wer: "12.14", benchmark_model: "org/a" }),
        capability("org/b", { benchmark_ami_wer: "7.13", benchmark_conversational_wer: "13.42" })];
    const profiles = [profile("Z profile", "org/a"), profile("A profile", "org/b"), profile("Missing checkpoint", "org/unknown")];
    const before = JSON.stringify(profiles);
    const choices = buildProfileChoices(profiles, models);
    assert.deepEqual(filterAndSortProfiles(choices, preferences(), "").map((choice) => choice.profile.id), ["A profile", "Z profile", "Missing checkpoint"]);
    assert.deepEqual(filterAndSortProfiles(choices, preferences({ sort: "conversational" }), "").map((choice) => choice.profile.id), ["Z profile", "A profile", "Missing checkpoint"]);
    assert.equal(choices[2].model.meetingWER, undefined);
    assert.equal(JSON.stringify(profiles), before);
    assert.equal(choices[0].profile.parameters, profiles[0].parameters);
});

test("a small Whisper profile cannot borrow large-v3 WER or memory", () => {
    const whisper = { ...capability("whisper", { benchmark_model: "large-v3", benchmark_ami_wer: "11.28", memory_model: "large-v3", gpu_float16_vram_gb: "4–5" }), model_family: "whisper" };
    const choices = buildProfileChoices([profile("Small", "small", { model_family: "whisper" }), profile("Large", "large-v3", { model_family: "whisper" })], [whisper]);
    assert.equal(choices[0].model.meetingWER, undefined);
    assert.equal(choices[0].gpuVRAM, undefined);
    assert.deepEqual(filterAndSortProfiles(choices, preferences(), "").map((choice) => choice.profile.id), ["Large", "Small"]);
});

test("saved run language and search combine without substituting a model's supported languages", () => {
    const choices = buildProfileChoices([profile("English", "org/a"), profile("French", "org/a", { language: "fr" }),
        profile("German", "org/a", { language: "de" }), profile("Detect", "org/a", { language: undefined })], [capability("org/a")]);
    assert.deepEqual(filterAndSortProfiles(choices, preferences({ language: "en" }), "").map((choice) => choice.profile.id), ["English"]);
    assert.deepEqual(filterAndSortProfiles(choices, preferences({ language: "other" }), "french gpu").map((choice) => choice.profile.id), ["French"]);
    assert.deepEqual(filterAndSortProfiles(choices, preferences({ language: "auto" }), "").map((choice) => choice.profile.id), ["Detect"]);
    assert.equal(filterAndSortProfiles(choices, preferences({ language: "fr" }), "nonexistent").length, 0);
    assert.ok(profileLanguageOptions(choices, "hi").some((option) => option.value === "hi"));
});

test("language WER compares only the requested language and exact model", () => {
    const comparison = (model: string, score: number) => JSON.stringify([{ model, publisher_coverage: "French", publisher_source: "https://example.com/model", wer: { fr: { mean_wer: score, datasets: { FLEURS: score } } } }]);
    const models = [capability("org/a", { benchmark_ami_wer: "1", language_comparisons: comparison("org/a", 10) }),
        capability("org/b", { benchmark_ami_wer: "20", language_comparisons: comparison("org/b", 5) })];
    const choices = buildProfileChoices([profile("A", "org/a", { language: "fr" }), profile("B", "org/b", { language: "fr" }), profile("Unknown", "org/unknown", { language: "fr" })], models);
    assert.deepEqual(filterAndSortProfiles(choices, preferences({ sort: "language_wer", language: "fr" }), "").map((choice) => choice.profile.id), ["B", "A", "Unknown"]);
});

test("GPU memory sorting uses saved precision and the upper estimate, with cloud and CPU-only models unranked", () => {
    const models = [capability("org/a", { supported_devices: "cpu,cuda", gpu_float16_vram_gb: "3–4", gpu_float32_vram_gb: "7–9" }),
        capability("org/cpu", { supported_devices: "cpu", gpu_float16_vram_gb: "1" })];
    const choices = buildProfileChoices([profile("FP32", "org/a", { compute_type: "float32" }), profile("FP16", "org/a"),
        profile("CPU only", "org/cpu"), profile("Cloud", "whisper-1", { model_family: "openai" })], models);
    assert.equal(choices[0].gpuVRAM, "7–9");
    assert.equal(choices[1].gpuVRAM, "3–4");
    assert.equal(choices[2].gpuVRAM, undefined);
    assert.equal(choices[3].gpuVRAM, undefined);
    assert.deepEqual(filterAndSortProfiles(choices, preferences({ sort: "gpu_memory" }), "").slice(0, 2).map((choice) => choice.profile.id), ["FP16", "FP32"]);
});

test("restoring selection uses the last valid choice, then user default, then available profile", () => {
    const profiles = [profile("A", "org/a"), profile("B", "org/a")];
    assert.equal(restoreProfileSelection(profiles, "B", "A"), "B");
    assert.equal(restoreProfileSelection(profiles, "deleted", "B"), "B");
    assert.equal(restoreProfileSelection(profiles, "deleted", "stale-default"), "A");
    assert.equal(restoreProfileSelection([], "B", "A"), "");
});

test("browser preferences validate unknown values and malformed storage", () => {
    assert.deepEqual(parseProfilePickerPreferences("invalid JSON"), preferences());
    assert.deepEqual(parseProfilePickerPreferences('{"sort":"invalid","language":42,"profileId":false}'), preferences());
    assert.deepEqual(parseProfilePickerPreferences('{"sort":"language_wer","language":"all","profileId":"B"}'), preferences({ profileId: "B" }));
    assert.deepEqual(parseProfilePickerPreferences('{"sort":"language_wer","language":"fr","profileId":"B"}'), preferences({ sort: "language_wer", language: "fr", profileId: "B" }));
});

test("browser persistence is scoped to the user, survives token rotation, and stores display choices only", () => {
    const token = (id: number, expiry: number) => `header.${btoa(JSON.stringify({ user_id: id, exp: expiry }))}.signature`;
    const key = profilePickerStorageKey(token(1, 1))!;
    assert.equal(key, profilePickerStorageKey(token(1, 2)));
    assert.notEqual(key, profilePickerStorageKey(token(2, 1)));
    assert.equal(profilePickerStorageKey(null), undefined);
    assert.equal(profilePickerStorageKey("malformed"), undefined);
    const values = new Map<string, string>();
    const storage = { getItem: (key: string) => values.get(key) || null, setItem: (key: string, value: string) => { values.set(key, value); } };
    const chosen = { ...preferences({ sort: "gpu_memory", language: "other", profileId: "B" }), parameters: { hf_token: "do not persist" } };
    saveProfilePickerPreferences(key, chosen, storage);
    assert.deepEqual(readProfilePickerPreferences(key, storage), preferences({ sort: "gpu_memory", language: "other", profileId: "B" }));
    assert.deepEqual(Object.keys(JSON.parse(values.get(key)!)).sort(), ["language", "profileId", "sort"]);
    assert.deepEqual(readProfilePickerPreferences(profilePickerStorageKey(token(2, 1)), storage), preferences());
    const blocked = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("blocked"); } };
    assert.deepEqual(readProfilePickerPreferences(key, blocked), preferences());
    assert.doesNotThrow(() => saveProfilePickerPreferences(key, chosen, blocked));
});
