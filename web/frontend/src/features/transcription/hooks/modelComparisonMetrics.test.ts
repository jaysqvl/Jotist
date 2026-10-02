import assert from "node:assert/strict";
import test from "node:test";
import { transcriptionModelChoices, type TranscriptionModelCapability } from "./modelCapabilities.ts";
import { diarizationChoices } from "./diarizationChoices.ts";
import { diarizationMetrics, transcriptionMetrics } from "./modelComparisonMetrics.ts";

const capability = (fields: Partial<TranscriptionModelCapability>): TranscriptionModelCapability => ({
    model_id: "test", model_family: "test", display_name: "Test", description: "", features: {}, ...fields,
});

test("compact comparisons retain exact Whisper benchmark and precision scope", () => {
    const whisper = capability({ model_id: "whisperx", model_family: "whisper", metadata: {
        benchmark_model: "large-v3", memory_model: "large-v3", benchmark_ami_wer: "0", benchmark_conversational_wer: "15.13",
        gpu_float16_vram_gb: "4–6", gpu_float32_vram_gb: "7–10",
    } });
    const choices = transcriptionModelChoices([whisper]);
    const large = choices.find((choice) => choice.model === "large-v3")!;
    assert.equal(transcriptionMetrics(large)[0].value, "0.00%");
    assert.equal(transcriptionMetrics(large, "float32")[2].value, "7–10 GB");
    assert.equal(transcriptionMetrics(large, "int8")[2].value, "—", "Do not borrow a floating-point memory estimate for quantized execution");
    assert.equal(transcriptionMetrics(choices.find((choice) => choice.model === "small")!)[0].value, "—");
});

test("CPU-only and cloud choices do not imply a local GPU estimate", () => {
    const bitnet = capability({ model_id: "vibevoice-bitnet", model_family: "vibevoice-bitnet", metadata: { supported_devices: "cpu", execution_location: "local" } });
    const choices = transcriptionModelChoices([bitnet]);
    assert.equal(transcriptionMetrics(choices.find((choice) => choice.model === "vibevoice-bitnet")!)[2].value, "CPU only");
    assert.equal(transcriptionMetrics(choices.find((choice) => choice.family === "openai")!)[3].value, "Cloud");
});

const pyannote = capability({ model_id: "pyannote", model_family: "pyannote", metadata: {
    model_id: "pyannote/speaker-diarization-community-1", benchmark_model: "pyannote/speaker-diarization-community-1",
    benchmark_der: "19.9", benchmark_der_dataset: "AMI SDM · Community-1",
    additional_benchmarks: JSON.stringify([{ model: "pyannote/speaker-diarization-3.1", metric: "DER", value: 22.4, dataset: "AMI array1/channel1", source: "https://example.com", provenance: "publisher" }]),
} });

test("alternate diarization checkpoints retain their own DER protocol", () => {
    assert.equal(diarizationMetrics(pyannote).metrics[0].value, "19.90%");
    const alternate = diarizationMetrics(pyannote, "pyannote/speaker-diarization-3.1");
    assert.equal(alternate.metrics[0].value, "22.40%");
    assert.match(alternate.note, /AMI array1\/channel1 · separate evaluation protocol/);
    assert.equal(diarizationMetrics(pyannote, "org/unknown").metrics[0].value, "—");
});

test("fixed Sortformer metadata yields memory only for its actual checkpoint", () => {
    const sortformer = capability({ model_id: "sortformer", model_family: "nvidia_sortformer", metadata: {
        memory_model: "nvidia/diar_streaming_sortformer_4spk-v2.1", benchmark_der: "17.8", benchmark_der_dataset: "AMI Test SDM",
        variant_memory_estimates: JSON.stringify([{ model: "nvidia/diar_streaming_sortformer_4spk-v2.1", gpu_float32_vram_gb: "3–7", gpu_memory_precision: "FP32", cpu_ram_gb: "4–7" }]),
    } });
    const metrics = diarizationMetrics(sortformer).metrics;
    assert.equal(metrics[0].value, "17.80%");
    assert.equal(metrics[1].value, "3–7 GB");
    assert.deepEqual(diarizationMetrics(sortformer, "org/unknown").metrics.map((metric) => metric.value), ["—", "—", "—"]);
});

test("speaker picker exposes checkpoints and preserves legacy selections without mutation", () => {
    const suplime = capability({ model_id: "suplime", model_family: "suplime", metadata: {
        model_id: "rewayai/suplime-large", model_variants: '["rewayai/suplime","rewayai/suplime-large"]',
    } });
    for (const current of [
        { diarize_model: "pyannote" },
        { diarize_model: "pyannote/speaker-diarization-3.1" },
        { diarize_model: "suplime", diarization_checkpoint: "rewayai/suplime" },
        { diarize_model: "suplime", diarization_checkpoint: "org/saved" },
    ]) {
        const before = structuredClone(current);
        const { choices, value } = diarizationChoices([pyannote, suplime], current);
        assert.deepEqual(current, before);
        assert.equal(choices.some((choice) => choice.value === value), true);
        assert.equal(choices.filter((choice) => choice.diarizer === "pyannote").length, 2);
        assert.equal(choices.some((choice) => choice.label === "SUPlime-L"), true);
        assert.equal(choices.some((choice) => choice.diarizer === "native"), false);
    }
    const native = diarizationChoices([pyannote], { diarize_model: "native" }, true);
    assert.equal(native.choices.find((choice) => choice.value === native.value)?.label, "Built into this model");
});
