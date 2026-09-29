import assert from "node:assert/strict";
import test from "node:test";
import { modelLanguageComparison, multilingualScore, offeredLanguageLabel, type ModelLanguageComparison } from "./modelLanguages.ts";
import { findModelCapability, transcriptionModelChoices, type TranscriptionModelCapability } from "./modelCapabilities.ts";

const comparison: ModelLanguageComparison = {
    model: "nvidia/parakeet-tdt-0.6b-v3", publisher_coverage: "25 European languages",
    publisher_source: "https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3",
    wer: { de: { mean_wer: 4, datasets: { FLEURS: 4 } }, fr: { mean_wer: 6, datasets: { FLEURS: 6 } } },
};
const capability = (changes: Partial<TranscriptionModelCapability> = {}): TranscriptionModelCapability => ({
    model_id: "parakeet", model_family: "nvidia_parakeet", display_name: "Parakeet", description: "", features: {}, supported_languages: ["en"],
    metadata: { language_comparison_checkpoint: comparison.model, language_comparisons: JSON.stringify([comparison]) }, ...changes,
});

test("macro scores require the complete chosen language set, preserving zero results", () => {
    assert.equal(multilingualScore(comparison, ["de", "fr"]), 5);
    assert.equal(multilingualScore(comparison, ["de", "fr", "hi"]), undefined);
    assert.equal(multilingualScore(comparison, []), undefined);
    assert.equal(multilingualScore(undefined, ["de"]), undefined);
    assert.equal(multilingualScore({ ...comparison, wer: { de: { mean_wer: 0, datasets: { FLEURS: 0 } } } }, ["de"]), 0);
});

test("fixed aliases resolve exact checkpoints, unknown repositories never inherit their scores", () => {
    for (const model of ["parakeet", "parakeet-tdt-0.6b-v3", comparison.model]) assert.equal(modelLanguageComparison(capability(), model)?.model, comparison.model);
    assert.equal(modelLanguageComparison(capability(), "nvidia/some-other-parakeet"), undefined);
    assert.equal(offeredLanguageLabel(capability(), "parakeet"), "English only");
});

test("Whisper variants and compressed checkpoints do not inherit a measured sibling", () => {
    const whisper = capability({ model_id: "whisperx", model_family: "whisper", supported_languages: ["auto", "en", "fr"], metadata: {
        language_comparison_checkpoint: "large-v3", language_comparisons: JSON.stringify([{ ...comparison, model: "large-v3" }, { ...comparison, model: "small.en", publisher_coverage: "English only", wer: {} }]),
    } });
    assert.equal(modelLanguageComparison(whisper, "small"), undefined);
    assert.deepEqual(modelLanguageComparison(whisper, "small.en")?.wer, {});
    assert.equal(offeredLanguageLabel(whisper, "small.en"), "English only");
    const compressed = capability({ model_id: "vibevoice-bitnet", model_family: "vibevoice_bitnet", metadata: {
        language_comparison_checkpoint: "microsoft/VibeVoice-ASR-BitNet", language_comparisons: JSON.stringify([{ ...comparison, model: "microsoft/VibeVoice-ASR-HF" }]),
    } });
    assert.equal(modelLanguageComparison(compressed, "vibevoice-bitnet"), undefined);
});

test("malformed optional metadata and invalid numbers cannot create a rank", () => {
    assert.equal(modelLanguageComparison(capability({ metadata: { language_comparisons: "not json" } })), undefined);
    const invalid = capability({ metadata: { language_comparison_checkpoint: comparison.model, language_comparisons: JSON.stringify([{ ...comparison, wer: { de: { mean_wer: -4 }, fr: { mean_wer: "6" } } }]) } });
    assert.deepEqual(modelLanguageComparison(invalid)?.wer, {});
});

test("retired saved Voxtral remains readable without substituting Mini 3B capabilities", () => {
    const voxtral = capability({ model_id: "mistralai/Voxtral-Mini-3B-2507", model_family: "mistral_voxtral", metadata: { benchmark_ami_wer: "13.57", meeting_recommendation_rank: "16", cpu_float32_ram_gb: "20–30" } });
    const retired = "mistralai/Voxtral-Mini-4B-Realtime-2602";
    assert.equal(findModelCapability([voxtral], "mistral_voxtral", retired), undefined);
    const choice = transcriptionModelChoices([voxtral], { model_family: "mistral_voxtral", model: retired }).find((row) => row.model === retired);
    assert.ok(choice);
    assert.match(choice.label, /Realtime.*saved selection/);
    assert.equal(choice.capability, undefined);
    assert.equal(choice.meetingWER, undefined);
    assert.equal(choice.recommendationRank, undefined);
    assert.equal(choice.cpuRAM, undefined);
    assert.equal(findModelCapability([voxtral], "mistral_voxtral", "voxtral"), voxtral);
    assert.equal(findModelCapability([voxtral], "mistral_voxtral", "mistralai/Voxtral-mini"), voxtral);
});
