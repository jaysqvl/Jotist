package transcription

import (
	"encoding/json"

	"scriberr/internal/transcription/adapters"
)

// Memory ranges are decimal GB planning allowances, not measured peaks or
// device-fit guarantees. Keep checkpoint rows separate: a family default must
// never stand in for an explicitly selected, differently sized checkpoint.
// Sources establish architecture/weights and defaults, not these allowances.
func diarizationMemoryEstimates(id string) []map[string]string {
	common := " Unmeasured allowance for one diarization worker, including runtime/activation headroom; excludes ASR and alignment. Full-recording audio, clustering and concurrent jobs can exceed the range."
	row := func(model, cpu, gpu, notes string) map[string]string {
		return map[string]string{
			"model": model, "cpu_float32_ram_gb": cpu, "cpu_memory_precision": "FP32",
			"gpu_float32_vram_gb": gpu, "gpu_memory_precision": "FP32",
			"fixed_precision": "float32", "estimate_status": "unmeasured",
			"notes": notes + common, "gpu_notes": notes + common,
			"source": "https://huggingface.co/" + model,
		}
	}
	switch id {
	case ModelPyannote:
		rows := []map[string]string{
			row("pyannote/speaker-diarization-community-1", "3–6", "12–16", "Community-1 segmentation and embedding run in FP32 with the checkpoint's default batches. The published model files total about 33 MB; most of this planning allowance is runtime headroom."),
			row("pyannote/speaker-diarization-3.1", "3–6", "12–16", "Legacy 3.1 segmentation and WeSpeaker embedding run in FP32 with the pipeline's default batches. This is a separate planning allowance, not a Community-1 measurement."),
		}
		for _, estimate := range rows {
			estimate["estimate_status"] = "planning_with_observations"
			estimate["gpu_notes"] = "Observed approximately 11.3 GB (10.5 GiB) total device peak on an RTX 3060 for a 15-second recording, FP32 and default pipeline batches, on 2026-09-20. Both selectable checkpoints were tested separately. The 12–16 GB range is a conservative planning allowance, not a measured long-recording bound. A 12 GiB card passed that clip with little headroom; longer recordings, runtime workspaces and concurrent GPU use can require more. This replaces the earlier 3–6 GB estimate, which underestimated the measured peak."
		}
		return rows
	case ModelSortformer:
		return []map[string]string{row("nvidia/diar_streaming_sortformer_4spk-v2.1", "4–7", "3–7", "Sortformer 2.1 runs in FP32, batch 1, with the 30.4-second input buffer. The published .nemo archive is about 0.47 GB; archive size is not peak runtime memory.")}
	case "diarizen":
		estimate := row("BUT-FIT/diarizen-wavlm-large-s80-md-v2", "5–16", "4–8", "DiariZen Large-s80-v2 runs in FP32 with the checkpoint's default batches. Its segmentation artifact is about 0.28 GB, plus a separate WeSpeaker embedding model. The publisher's 63.3M count covers only the pruned WavLM backbone.")
		estimate["estimate_status"] = "planning_with_observations"
		estimate["notes"] = "Observed 13.1 GB (12.2 GiB) peak across the owned CPU worker processes on a Ryzen 7 5700G for a 28-minute recording, FP32 and default batches, on 2026-09-20. A 15-second clip used about 1.8 GB. The 5–16 GB range is a planning allowance informed by those runs, not a bound for longer meetings; it excludes the ASR and alignment workers. This replaces the earlier 5–9 GB estimate, which underestimated the full-meeting peak."
		return []map[string]string{estimate}
	case "suplime":
		rows := []map[string]string{
			row("rewayai/suplime", "5–9", "4–8", "SUPlime uses about 139M parameters including its embedding model, about 0.56 GB of FP32 weights. Planning assumes segmentation/embedding batch 32 and 10-second segmentation windows."),
			row("rewayai/suplime-large", "7–13", "6–12", "SUPlime-L uses about 374M parameters including its embedding model, about 1.50 GB of FP32 weights. Planning assumes segmentation/embedding batch 32 and 10-second segmentation windows."),
		}
		for _, estimate := range rows {
			// SUPLIME_FP16 controls encoder autocast, not weight storage. It is
			// enabled by upstream on CUDA; our CPU runner explicitly disables it.
			estimate["gpu_vram_gb"] = estimate["gpu_float32_vram_gb"]
			delete(estimate, "gpu_float32_vram_gb")
			delete(estimate, "fixed_precision")
			estimate["gpu_memory_precision"] = "FP32 weights + FP16 encoder autocast"
			estimate["gpu_notes"] += " CUDA defaults to FP16 autocast only inside the WavLM encoder; downstream work and resident weights stay FP32. SUPLIME_FP16=0 disables autocast and can require more VRAM."
		}
		return rows
	}
	return nil
}

func addRuntimeMemoryMetadata(metadata map[string]string, id string) {
	if rows := diarizationMemoryEstimates(id); len(rows) > 0 {
		metadata["variant_memory_estimates"] = memoryJSON(rows)
		model := metadata["model_id"]
		if model == "" {
			model = rows[0]["model"]
			if id == "suplime" {
				model = "rewayai/suplime-large"
			}
		}
		for _, row := range rows {
			if row["model"] != model {
				continue
			}
			for _, key := range []string{"cpu_float32_ram_gb", "cpu_memory_precision", "gpu_float32_vram_gb", "gpu_vram_gb", "gpu_memory_precision", "fixed_precision", "estimate_status"} {
				if value := row[key]; value != "" {
					metadata[key] = value
				}
			}
			metadata["memory_model"] = model
			metadata["memory_estimate_notes"] = row["notes"]
			metadata["gpu_memory_estimate_notes"] = row["gpu_notes"]
			metadata["memory_source"] = row["source"]
		}
	}
	if id == ModelParakeet {
		// The NeMo Parakeet runner neither casts weights nor uses autocast;
		// compute_type does not affect it. TF32 is an FP32 math optimization.
		delete(metadata, "gpu_float16_vram_gb")
		metadata["fixed_precision"] = "float32"
		metadata["supported_precisions"] = "float32"
		metadata["gpu_memory_precision"] = "FP32 (TF32 math enabled)"
		metadata["gpu_memory_estimate_notes"] = "Unmeasured FP32 planning range for batch 1 and short chunks: approximately 0.6B parameters at 4 bytes each plus 2–6 GB runtime headroom. This runner enables TF32 CUDA math but does not cast weights to FP16; compute_type does not change its precision. Long recordings and concurrent workers may need more."
	}
	if alignment := alignmentMemoryEstimate(id); alignment != nil {
		metadata["alignment_memory_estimates"] = memoryJSON(alignment)
	}
}

// alignment_memory_estimates is a JSON object, using the same range fields as
// variant_memory_estimates. device_policy is "same_as_asr" or "cpu". Missing
// numerical ranges mean unknown/unsupported, never zero memory consumption.
func alignmentMemoryEstimate(id string) map[string]string {
	if id == ModelCanary {
		return map[string]string{
			"model": "Canary embedded NeMo CTC aligner", "device_policy": "same_as_asr",
			"stage_order": "after_asr", "estimate_status": "unknown", "fixed_precision": "float32",
			"cpu_memory_precision": "FP32", "gpu_memory_precision": "FP32",
			"notes":  "A separate stage loads Canary's embedded CTC model and tokenizer after recognition is released. CTC uses FP32 on CPU or GPU, even when recognition uses FP16. No general RAM or VRAM estimate is available; native alignment batch size, audio duration and permitted adaptive alignment settings affect memory.",
			"source": "https://huggingface.co/nvidia/canary-1b-v2",
		}
	}
	if id == ModelWhisperX {
		return map[string]string{
			"model": "language-dependent WhisperX alignment model", "device_policy": "same_as_asr",
			"stage_order": "after_asr", "estimate_status": "unknown", "cpu_memory_precision": "FP32", "gpu_memory_precision": "FP32",
			"notes":  "WhisperX chooses a separate alignment model by language (or align_model override). Its memory has not been estimated per checkpoint. Alignment follows ASR; CPU RAM and GPU VRAM still need runtime/audio headroom.",
			"source": "https://github.com/m-bain/whisperX/blob/v3.8.6/whisperx/alignment.py",
		}
	}
	usesQwen := id == "vibevoice-bitnet" || id == ModelVoxtral
	for _, spec := range adapters.LocalASRModels() {
		if spec.ID == id {
			// Granite Plus returns native word timestamps; the runner skips
			// forced alignment even when align_words is requested.
			usesQwen = spec.Engine != "granite_plus"
			break
		}
	}
	if !usesQwen {
		return nil
	}
	// HF Safetensors metadata, inspected 2026-09-12, counts audio components:
	// 917,728,896 BF16 parameters, despite the checkpoint's "0.6B" short name.
	// Two/four bytes per parameter plus 2–6 GB headroom, rounded up.
	row := map[string]string{
		"model": "Qwen/Qwen3-ForcedAligner-0.6B-hf", "weights_parameters_b": "0.917728896",
		"device_policy": "same_as_asr", "stage_order": "after_asr", "estimate_status": "unmeasured",
		"cpu_float32_ram_gb": "6–10", "gpu_float32_vram_gb": "6–10", "gpu_float16_vram_gb": "4–8",
		"cpu_memory_precision": "FP32",
		"notes":                "Unmeasured allowance for optional word alignment, batch 1 and up to 30-second windows. Complete checkpoint weights are about 3.67 GB in FP32 or 1.84 GB in FP16/BF16, plus 2–6 GB runtime headroom. ASR unloads before alignment; this stage uses the ASR device and precision. Longer alignment windows can exceed this range.",
		"source":               "https://huggingface.co/Qwen/Qwen3-ForcedAligner-0.6B-hf",
	}
	if id == "vibevoice-bitnet" {
		row["device_policy"] = "cpu"
		row["fixed_precision"] = "float32"
		delete(row, "gpu_float32_vram_gb")
		delete(row, "gpu_float16_vram_gb")
		row["notes"] = "Unmeasured CPU FP32 allowance for optional word alignment, batch 1 and 30-second windows: approximately 3.67 GB of complete checkpoint weights plus 2–6 GB runtime headroom. BitNet's quantized ASR process exits before this separate CPU aligner starts; this adapter does not run alignment on GPU."
	}
	return row
}

func memoryJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
