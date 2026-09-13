# Canary staged recovery qualification

Validated on **2026-09-12** with isolated CPU and CUDA adapter workers. This is execution and output-parity evidence, not meeting accuracy measurement or a production deployment. Machine-readable comparisons and measurements are in [the qualification snapshot](canary-stage-qualification-2026-09-12.json).

## Implemented boundary

Recognition uses the existing NeMo timestamp prompt, native chunk selection, overlap, language configuration and decoding. It defers only the CTC timestamp call. Its checkpoint retains raw hypotheses, original cut IDs/sample offsets, encoded lengths, exact source sample hashes, native CTC batch size, and outer manual-chunk offsets. The top-level normalized text is a partial preview until timestamp merging completes; it is not represented as a completed aligned transcript.

The recognition subprocess exits before alignment starts. Alignment restores only the **existing embedded CTC weights and exact tokenizer** extracted from the original Canary archive. The main recognizer weights are excluded from that extraction. It reconstructs and verifies every source cut, calls the original NeMo CTC timestamp helper, and performs the original native chunk/outer-cut merges. Subsecond source padding follows the native centered one-second Lhotse preprocessing and is verified against captured float32 sample hashes.

The archive is pinned to HF revision `d455706339a6b32e1aa40f82c713a482a0c938e2`, SHA256 `ae5ef1bf06812a95a1594a8f5f0ee9c51f35418e5ba96939fa6b98ab00431094`. The CTC weights SHA256 is `2155f5642f9d27d73bd0c41693ddbb2420a95b32acc11576c9b36638f7ece795`. Cached artifacts and the five relevant NeMo source files are verified before execution. Changing an artifact or runtime requires requalification rather than silently attaching the old identity.

Native CTC uses FP32 even when recognition uses FP16. Its initial batch is the number of native audio chunks, **not** the recognizer's requested batch size. Alignment resolves that count from the recognition checkpoint before recording effective parameters.

## Actual comparisons

The source is the public `mary_had_lamb` recording from [`hf-internal-testing/dummy-audio-samples`](https://huggingface.co/datasets/hf-internal-testing/dummy-audio-samples). The boundary fixture concatenates five 15-second copies with 0.5 seconds of silence after each, for **77.5 seconds**. Native inference selected two overlapping cuts: 40 seconds and 38.5 seconds, beginning at 0 and 39 seconds. No private recording was read.

| Comparison | Text / word surface | Word and segment timestamps |
| --- | --- | --- |
| CPU FP32 original vs split, 15s | Exact; 37 words | Exact |
| CPU FP32 original vs split, 77.5s | Exact; 196 words | Exact |
| CPU CTC native batch 2 vs batch 1 | Exact | Exact |
| GPU FP16 recognizer + FP32 CTC original vs split, 77.5s | Exact; 198 words | Exact |
| GPU CTC native batch 2 vs batch 1 | Exact | Exact |
| GPU recognition checkpoint → CPU FP32 CTC | Exact; 198 words | Exact on this fixture |

CPU FP32 recognition produced 196 words; GPU FP16 produced 198. That numerical-policy difference existed in both the original and split pipelines. It is not normalized away or described as cross-precision equivalence.

An additional real 0.5-second source/recognition/alignment check passed after correcting native centered-padding reconstruction.

## Qualified adaptation

**Batch:** CTC alignment may reduce its recorded native batch to 1. Recognition batch reduction is not declared qualified: one source expands to several native chunks independently of the outer requested batch size. No quality equivalence is inferred from a field merely being called “batch.”

**CPU:** the independent recognition and CTC stages support explicit CPU FP32. Device or precision changes remain coordinator-controlled. Workers never silently switch devices.

**Shorter windows:** only the CTC acoustic encoder may use 20- or 10-second input windows, including context. At least two seconds of context is retained on each side; two-second per-side context gives four-second overlap between adjacent input windows. A 20-second window retains a 16-second center, and a 10-second window retains a six-second center. Centers tile the exact 80ms CTC frame grid once, without invented padding or dropped frames. The original full-native-cut Viterbi alignment, saved recognition text, tokenizer and native transcript merge are retained. Recognition and speaker windows remain unchanged. This bounds acoustic-encoder input, not the entire Viterbi allocation.

The worker uses NeMo's existing `has_hypotheses=True` contract to supply the stitched logits to the unchanged timestamp helper. It rejects unexpected frame cardinality. Both window sizes preserved every word on CPU and GPU. Against native CTC, the largest word-boundary change was **0.24 seconds**; mean changes were about **0.023–0.026 seconds**. This is measured fixture behavior, not a guaranteed timing bound on other recordings. Reduced acoustic context can affect alignment accuracy and requires explicit Level 4 permission.

## Measured memory and failure recovery

Hardware: Ryzen 7 5700G and RTX 3060, isolated containers limited to six CPUs and 32 GiB RAM. CPU used Torch `2.8.0+cpu`; GPU used `2.8.0+cu126`. Every audio-processing container had networking disabled. The CUDA environment was created separately; production configuration and the existing CPU runtime were not changed.

| GPU operation, 77.5s | Torch peak allocated VRAM |
| --- | ---: |
| Original combined recognition + CTC | 4.701 GiB |
| Independent FP16 recognition | 2.073 GiB |
| Independent FP32 CTC, native batch 2 | 2.876 GiB |
| Independent FP32 CTC, batch 1 | 2.629 GiB |
| CTC, batch 1, 20-second inputs | 2.503 GiB |
| CTC, batch 1, 10-second inputs | 2.442 GiB |

These are **Torch allocator peaks**, not total device memory. Subprocess stages run sequentially, so their peaks are not added. CPU process peak RSS was approximately 7.89 GiB for recognition and 5.47 GiB for CTC. Model loading dominates the short CTC RSS figures; smaller acoustic windows did not materially reduce that CPU peak. Measurements include loading and execution and do not establish capacity for an hour-long meeting.

An isolated GPU alignment process was restricted to 10% of its device allocator budget. It raised a real `torch.OutOfMemoryError`, emitted the structured `cuda_out_of_memory` marker, and created no alignment result. Its recognition checkpoint remained intact. A fresh CPU FP32 alignment process consumed that exact checkpoint, loaded only CTC, and recovered all 198 words and timestamps exactly on this fixture. This verifies the worker boundary and failure evidence; queue retries and policy authorization are covered separately by coordinator integration tests.

Private chunk WAVs and model-loader temporaries belong to the Go attempt directory (`TMPDIR` plus explicit Python parents), allowing cleanup after a process-group cancellation. Tests cover actual subprocess cancellation, source-hash rejection, archive integrity, frame ownership, and the API argument contract.

## Limits and source references

This qualification covers the stated public samples, source/runtime hashes, devices, batch change, and window candidates. It does not score meeting WER or speaker DER, validate every supported language, prove equivalence for arbitrary longer input, or exercise the native hour-boundary merge on an hour-long recording. Private meeting recordings and the production service were not used.

The original source contracts are [NeMo v2.7.3 Canary output processing](https://github.com/NVIDIA/NeMo/blob/v2.7.3/nemo/collections/asr/models/aed_multitask_models.py), [native audio chunking](https://github.com/NVIDIA/NeMo/blob/v2.7.3/nemo/collections/asr/data/audio_to_text_lhotse_prompted.py), [timestamp helper](https://github.com/NVIDIA/NeMo/blob/v2.7.3/nemo/collections/asr/parts/utils/timestamp_utils.py), [alignment variables and Viterbi](https://github.com/NVIDIA/NeMo/blob/v2.7.3/nemo/collections/asr/parts/utils/aligner_utils.py), and [native chunk merging](https://github.com/NVIDIA/NeMo/blob/v2.7.3/nemo/collections/asr/parts/utils/chunking_utils.py). The model is [NVIDIA Canary-1B-v2](https://huggingface.co/nvidia/canary-1b-v2).
