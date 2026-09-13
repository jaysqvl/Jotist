import assert from "node:assert/strict";
import test from "node:test";
import { applyDeviceSelectionDefaults, applyModelSelectionDefaults, type SelectionDefaultParams } from "./selectionDefaults.ts";
import type { TranscriptionModelCapability } from "./modelCapabilities.ts";

const capability = (model_family: string, model_id: string, changes: Partial<TranscriptionModelCapability> = {}): TranscriptionModelCapability => ({
    model_family, model_id, display_name: model_id, description: "", features: {}, supported_languages: ["en", "fr"], ...changes,
});
const qwen = capability("qwen3_asr", "Qwen/Qwen3-ASR-1.7B-hf", { metadata: { default_chunk_duration: "30", max_chunk_duration: "30", default_max_new_tokens: "0", timestamp_source: "forced_alignment" } });
const smallQwen = { ...qwen, model_id: "Qwen/Qwen3-ASR-0.6B-hf" };
const canary = capability("nvidia_canary", "canary");
const canaryQwen = capability("nvidia_canary_qwen", "canary_qwen", { supported_languages: ["en"] });
const parakeet = capability("nvidia_parakeet", "parakeet", { supported_languages: ["en"] });
const bitnet = capability("vibevoice-bitnet", "vibevoice-bitnet", { supported_languages: ["*"], metadata: { supported_devices: "cpu", fixed_precision: "i2_s+i8_s", fixed_chunk_duration: "30", default_max_new_tokens: "16384" } });
const moss = capability("moss_asr", "OpenMOSS-Team/MOSS-Transcribe-Diarize", {
    features: { integrated_diarization: true },
    metadata: { default_chunk_duration: "0", max_chunk_duration: "5400", default_max_new_tokens: "0", timestamp_source: "native_segments_optional_word_alignment" },
});
const models = [qwen, smallQwen, canary, canaryQwen, parakeet, bitnet, moss];
const choice = (model: TranscriptionModelCapability) => ({ family: model.model_family, model: model.model_id, capability: model });
const params = (): SelectionDefaultParams & { hf_token_source: string; hf_token: string; transcription_context: string | null; transcription_context_terms: string | null; beam_size: number; api_key: string } => ({
    model_family: "whisper", model: "large-v3", device: "cpu", compute_type: "int8", batch_size: 8,
    task: "translate", language: "fr", fp16: false, no_align: true, chunk_size: 80,
    audio_chunk_duration: 300, max_new_tokens: 8192, attention_context_left: 512, attention_context_right: 512,
    nvidia_precision: "bfloat16", nvidia_chunk_duration: 300, nvidia_use_chunking: false, nvidia_timestamps: false,
    diarize: true, diarize_model: "suplime", diarization_checkpoint: "rewayai/suplime-large", diarization_device: "cuda",
    hf_token_source: "custom", hf_token: "synthetic-test-token", transcription_context: "Software meeting", transcription_context_terms: null,
    beam_size: 7, api_key: "synthetic-api-key",
});

test("Whisper device changes choose FP16 GPU and FP32 CPU with a bounded batch without altering decoding or secrets", () => {
    const original = params();
    const gpu = applyDeviceSelectionDefaults(original, "cuda", models);
    assert.equal(gpu.device, "cuda");
    assert.equal(gpu.compute_type, "float16");
    assert.equal(gpu.fp16, true);
    assert.equal(gpu.nvidia_precision, "float16");
    assert.equal(gpu.batch_size, 1);
    for (const key of ["task", "language", "no_align", "max_new_tokens", "beam_size", "hf_token_source", "hf_token", "transcription_context", "transcription_context_terms", "api_key", "diarization_checkpoint"] as const) assert.equal(gpu[key], original[key]);
    const cpu = applyDeviceSelectionDefaults(gpu, "cpu", models);
    assert.equal(cpu.compute_type, "float32");
    assert.equal(cpu.nvidia_precision, "float32");
    assert.equal(cpu.fp16, false);
    assert.equal(original.compute_type, "int8");
    assert.equal(original.batch_size, 8);
});

test("Canary GPU selection and device changes enable 40-second chunks and retain the external diarizer on CPU", () => {
    const original = { ...params(), device: "cuda" };
    const selected = applyModelSelectionDefaults(original, choice(canary), models);
    assert.equal(selected.device, "cuda");
    assert.equal(selected.nvidia_precision, "float16");
    assert.equal(selected.compute_type, "float16");
    assert.equal(selected.batch_size, 1);
    assert.equal(selected.nvidia_chunk_duration, 40);
    assert.equal(selected.nvidia_use_chunking, true);
    assert.equal(selected.nvidia_timestamps, true);
    assert.equal(selected.task, "transcribe");
    assert.equal(selected.nvidia_target_language, "fr");
    assert.equal(selected.diarization_device, "cpu");
    assert.equal(selected.diarize_model, original.diarize_model);
    assert.equal(selected.diarization_checkpoint, original.diarization_checkpoint);
    const gpu = applyDeviceSelectionDefaults({ ...selected, device: "cpu", nvidia_use_chunking: false, nvidia_chunk_duration: 300 }, "cuda", models);
    assert.equal(gpu.nvidia_chunk_duration, 40);
    assert.equal(gpu.nvidia_use_chunking, true);
    assert.equal(applyDeviceSelectionDefaults(gpu, "cpu", models).nvidia_precision, "float32");
});

test("new family and exact variant changes preserve CUDA and external speaker choices while resetting incompatible ASR windows", () => {
    const original = { ...params(), device: "cuda" };
    const selected = applyModelSelectionDefaults(original, choice(qwen), models);
    assert.equal(selected.model, qwen.model_id);
    assert.equal(selected.device, "cuda");
    assert.equal(selected.compute_type, "float16");
    assert.equal(selected.audio_chunk_duration, 30);
    assert.equal(selected.max_new_tokens, 0);
    assert.equal(selected.no_align, false);
    assert.equal(selected.diarize, true);
    assert.equal(selected.diarize_model, original.diarize_model);
    assert.equal(selected.diarization_checkpoint, original.diarization_checkpoint);
    assert.equal(selected.diarization_device, original.diarization_device);
    assert.equal(selected.hf_token, original.hf_token);
    assert.equal(selected.transcription_context, original.transcription_context);
    const smaller = applyModelSelectionDefaults({ ...selected, audio_chunk_duration: 20, max_new_tokens: 500 }, choice(smallQwen), models);
    assert.equal(smaller.device, "cuda");
    assert.equal(smaller.audio_chunk_duration, 30);
    assert.equal(smaller.max_new_tokens, 0);
    assert.equal(smaller.diarization_checkpoint, original.diarization_checkpoint);
});

test("CPU-only BitNet selects fixed quantization and alignment without retaining GPU precision or long ASR chunks", () => {
    const selected = applyModelSelectionDefaults({ ...params(), device: "cuda" }, choice(bitnet), models);
    assert.equal(selected.device, "cpu");
    assert.equal(selected.compute_type, "i2_s+i8_s");
    assert.equal(selected.fp16, false);
    assert.equal(selected.audio_chunk_duration, 30);
    assert.equal(selected.max_new_tokens, 16384);
    assert.equal(selected.no_align, false);
    assert.equal(selected.language, "fr");
    const rejectedGPU = applyDeviceSelectionDefaults(selected, "cuda", models);
    assert.equal(rejectedGPU.device, "cpu");
    assert.equal(rejectedGPU.compute_type, "i2_s+i8_s");
});

test("NVIDIA defaults follow each runtime's token, precision and attention contracts", () => {
    const selected = applyModelSelectionDefaults({ ...params(), device: "cuda", nvidia_prompt: "" }, choice(canaryQwen), models);
    assert.equal(selected.nvidia_precision, "float16");
    assert.equal(selected.nvidia_chunk_duration, 40);
    assert.equal(selected.max_new_tokens, 256);
    assert.equal(selected.nvidia_prompt, "Transcribe the following:");
    assert.equal(selected.language, "en");
    const parakeetSelection = applyModelSelectionDefaults(selected, choice(parakeet), models);
    assert.equal(parakeetSelection.device, "cuda");
    assert.equal(parakeetSelection.compute_type, "float32");
    assert.equal(parakeetSelection.nvidia_precision, "float32");
    assert.equal(parakeetSelection.fp16, false);
    assert.equal(parakeetSelection.attention_context_left, 256);
    assert.equal(parakeetSelection.attention_context_right, 256);
    assert.equal(parakeetSelection.nvidia_chunk_duration, 300);
    assert.equal(parakeetSelection.max_new_tokens, undefined);
});

test("native speaker entry, external preservation and exit use valid timing and speaker settings", () => {
    const selected = applyModelSelectionDefaults({ ...params(), diarize: false }, choice(moss), models);
    assert.equal(selected.diarize_model, "native");
    assert.equal(selected.diarize, true);
    assert.equal(selected.diarization_checkpoint, undefined);
    assert.equal(selected.audio_chunk_duration, 0);
    assert.equal(selected.no_align, true);
    const external = applyModelSelectionDefaults(params(), choice(moss), models);
    assert.equal(external.diarize_model, "suplime");
    assert.equal(external.diarization_checkpoint, "rewayai/suplime-large");
    const leaving = applyModelSelectionDefaults(selected, choice(qwen), models);
    assert.equal(leaving.diarize, true);
    assert.equal(leaving.diarize_model, "pyannote");
    assert.equal(leaving.diarization_checkpoint, "pyannote/speaker-diarization-community-1");
    assert.equal(leaving.no_align, false);
    assert.equal(leaving.audio_chunk_duration, 30);
    const canaryExit = applyModelSelectionDefaults({ ...selected, device: "cuda", diarization_device: "cuda" }, choice(canary), models);
    assert.equal(canaryExit.diarize_model, "pyannote");
    assert.equal(canaryExit.diarization_device, "cpu");
    assert.equal(applyModelSelectionDefaults(params(), choice(moss), models, true).diarize, false);
});

test("cloud selection disables speakers and stale native mode, while retaining credentials for returning to local ASR", () => {
    const native = applyModelSelectionDefaults({ ...params(), diarize: false }, choice(moss), models);
    const cloud = applyModelSelectionDefaults(native, { family: "openai", model: "whisper-1" }, models);
    assert.equal(cloud.diarize, false);
    assert.equal(cloud.diarize_model, "pyannote");
    assert.equal(cloud.diarization_checkpoint, "pyannote/speaker-diarization-community-1");
    assert.equal(cloud.hf_token, native.hf_token);
    assert.equal(cloud.api_key, native.api_key);
    assert.equal(cloud.transcription_context, native.transcription_context);
    const local = applyModelSelectionDefaults(cloud, choice(qwen), models);
    assert.equal(local.diarize, false);
    assert.equal(local.no_align, false);
});

test("Auto requests GPU FP16 for backend CPU fallback and capability restrictions override unsupported devices", () => {
    const selected = applyModelSelectionDefaults({ ...params(), device: "auto" }, choice(qwen), models);
    assert.equal(selected.device, "auto");
    assert.equal(selected.compute_type, "float16");
    assert.equal(selected.nvidia_precision, "float16");
    assert.equal(selected.fp16, true);
    const gpuOnly = capability("future_gpu_asr", "org/gpu-only", { requires_gpu: true });
    assert.equal(applyModelSelectionDefaults(params(), choice(gpuOnly), [gpuOnly]).device, "cuda");
    const english = applyModelSelectionDefaults({ ...selected, language: "fr" }, { family: "whisper", model: "small.en" }, models);
    assert.equal(english.language, "en");
    assert.equal(english.device, "auto");
    assert.equal(english.compute_type, "float16");
    assert.equal(english.chunk_size, 30);
    for (const model of [canary, canaryQwen]) {
        const automatic = applyDeviceSelectionDefaults(applyModelSelectionDefaults(params(), choice(model), models), "auto", models);
        assert.equal(automatic.device, "auto");
        assert.equal(automatic.nvidia_precision, "float16");
        assert.equal(automatic.batch_size, 1);
        assert.equal(automatic.nvidia_chunk_duration, 40);
        assert.equal(automatic.nvidia_use_chunking, true);
        assert.equal(automatic.diarization_device, "cpu");
    }
    const autoParakeet = applyModelSelectionDefaults({ ...params(), device: "auto" }, choice(parakeet), models);
    assert.equal(autoParakeet.device, "auto");
    assert.equal(autoParakeet.compute_type, "float32");
    assert.equal(autoParakeet.fp16, false);
    const autoBitNet = applyModelSelectionDefaults({ ...params(), device: "auto" }, choice(bitnet), models);
    assert.equal(autoBitNet.device, "cpu");
    assert.equal(autoBitNet.compute_type, "i2_s+i8_s");
});

test("unchanged selections and input objects retain saved custom settings", () => {
    const saved = { ...params(), model_family: qwen.model_family, model: qwen.model_id };
    assert.equal(applyModelSelectionDefaults(saved, choice(qwen), models), saved);
    assert.equal(applyDeviceSelectionDefaults(saved, saved.device, models), saved);
    const snapshot = structuredClone(saved);
    applyModelSelectionDefaults(saved, choice(bitnet), models);
    assert.deepEqual(saved, snapshot);
});
