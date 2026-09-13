# Local speech integration validation

Validation date: **2026-09-12 UTC**. Published benchmark results and memory estimates are explained separately in [Reading the model comparison](model-comparison.md).

## Actual model inference

All **12 shared ASR variants** completed genuine CPU FP32 inference on the public, approximately 15-second `mary_had_lamb` sample from Hugging Face's `hf-internal-testing/dummy-audio-samples` dataset. The host used an AMD Ryzen 7 5700G; the isolated container was limited to **6 CPUs and 32 GB RAM**.

These runs exercised checkpoint loading, recognition and output serialization. Qwen3-ASR 1.7B also ran the separate Qwen3 forced aligner. Other variants ran without forced alignment; MOSS Diarize and Granite Plus supplied their native timing output.

For long CPU recordings, review the existing [job timeout setting](model-comparison.md#long-cpu-jobs): `MEDIA_PROCESS_TIMEOUT_MINUTES` defaults to 120 and can be raised to 1440 for queued transcription and media subprocesses.

| Checkpoint | CPU FP32 result | Timing exercised | Isolated child peak RSS |
| --- | --- | --- | ---: |
| `Qwen/Qwen3-ASR-1.7B-hf` | Passed | Forced word alignment | 12,878,776 KiB |
| `Qwen/Qwen3-ASR-0.6B-hf` | Passed | Unaligned | 5,428,404 KiB |
| `ibm-granite/granite-speech-4.1-2b` | Passed | Unaligned | — |
| `ibm-granite/granite-speech-4.1-2b-plus` | Passed | Native word ends | — |
| `ibm-granite/granite-speech-5.0-470m-turboctc` | Passed | Unaligned | 3,453,988 KiB |
| `ibm-granite/granite-speech-5.0-470m-turboctc-nc` | Passed | Unaligned | 3,431,808 KiB |
| `CohereLabs/cohere-transcribe-03-2026` | Passed | Unaligned | 12,800,928 KiB |
| `Edge0/ARK-ASR-3B` | Passed | Unaligned | — |
| `OpenMOSS-Team/MOSS-Transcribe-Diarize` | Passed | Native segments and speaker labels | — |
| `OpenMOSS-Team/MOSS-Transcribe-preview-2B` | Passed | Unaligned | 14,604,712 KiB |
| `mistralai/Voxtral-Mini-3B-2507` | Passed | Unaligned | — |
| `mistralai/Voxtral-Mini-4B-Realtime-2602` | Passed | Unaligned | — |

“Unaligned” means known audio-window bounds, without inferred word times. Granite Plus predicts word ends; each word's start uses the preceding emitted end. Native speaker output was exercised, but its attribution accuracy was not scored.

Only the six reliable per-child RSS measurements are reported. Other peaks are omitted because the initial driver accumulated maxima from earlier children instead of isolating each model. The Qwen 1.7B measurement includes recognition followed by alignment, with the recognizer unloaded before the aligner loads. These short-sample peaks do not establish memory requirements for a long meeting or concurrent jobs. Runtime durations are not compared because runs included loading and, where needed, downloads.

The MOSS Preview retry passed after compatibility fixes for Transformers 5 weight tying and cached generation. Its successful load report contained no missing `lm_head.weight` entry or runtime traceback.

The actual Go adapters also completed first-use setup and CPU inference for the following runtimes in the same isolated container limit:

| Adapter/checkpoint | Result |
| --- | --- |
| VibeVoice ASR BitNet 1.5B | Pinned C++ source built successfully; quantized recognition followed by Qwen3 alignment produced 37 aligned words |
| DiariZen Large-s80-v2 | Passed; speaker intervals and labels parsed |
| SUPlime | Passed; speaker intervals and labels parsed |
| SUPlime-L | Passed; speaker intervals and labels parsed |
| Pyannote Community-1 | Passed; speaker intervals and labels parsed |
| Pyannote 3.1 (legacy) | Passed with the updated Pyannote runtime; explicit checkpoint selection retained |
| Sortformer 2.1 | Passed; speaker intervals and labels parsed |

All seven adapter/checkpoint combinations reported the actual execution device as CPU. The diarizers sometimes detected different speaker counts on the noisy sample; success here means a functioning inference/output path, not correct attribution or a measured DER. The BitNet path uses fixed quantized weights, rather than the FP32 arithmetic used by the twelve shared ASR variants above.

The updated legacy recognizers also passed actual CPU inference: Whisper large-v3 with word alignment, Community-1 diarization, an initial prompt and vocabulary; Canary 1B v2; Parakeet TDT 0.6B v3; and Canary-Qwen 2.5B with context. Whisper's retry passed after correcting the CPU TorchVision wheel source. Both standalone Pyannote checkpoints also passed with the normal API's VAD probability settings; unsupported powerset probability thresholds are skipped without changing gap-duration parameters.

## Local and browser checks

- `go test ./...` passed. Shared ASR environment tests also passed under the race detector, covering first-use preparation, concurrent adapters, stale runtime refresh and cancellation.
- The local ASR Python suite passed **45 tests**. These use lightweight doubles to check publisher API arguments, explicit device/precision handling, token-budget cutoffs, contiguous audio windows, alignment offsets, original spelling/punctuation, supported alignment languages, model unloading, temporary-file ownership and overlapping native speaker labels. They are separate from the real inference runs above.
- The frontend passed **51 tests**, full lint/type checks and its production build. Frontend and project-site dependency audits reported zero vulnerabilities; project-site lint and build passed too.
- The CI Go vet/test commands passed. Adapter race checks cover shared legacy preparation and atomic project-file replacement. Eleven dependency-free runtime regressions and all seven environment dependency checks passed. API and repository tests also passed under the race detector after the profile persistence changes.
- CUDA 12.6 and 12.8 dependency resolution passed for the pinned shared ASR runtime. This checks package availability and compatibility constraints, not GPU inference.
- Authenticated browser checks against the actual API passed for settings and profile flows. These establish rendered controls and persistence behavior; they do not constitute a meeting transcription accuracy test.

Context checks cover inherited defaults, explicit empty overrides, snapshots at queue admission and capability filtering. Supported models receive prose and/or vocabulary according to their declared capabilities. Unsupported models receive neither. These checks establish that the requested hints reach the intended recognizer; they do not establish an accuracy improvement from those hints.

The final preview was served on the host LAN address and reached successfully from the NAS. Rendered desktop and 390 × 844 mobile checks covered Quick Add Presets, hardware reference text, duplicate prevention, prefilled CPU profile settings and the recommendation-ordered model picker. Three reference presets were added through the UI; readback matched all 153 sanitized technical values. A separately saved user default remained selected after another preset was added. The nine reference definitions also matched all 459 sanitized technical values from a read-only production profile export. Credentials, private prompts and paths are excluded; context and Hugging Face access inherit the receiving user's Settings.

Browser checks confirmed Whisper small's publisher and community results, their separate protocols and sources, and its checkpoint-specific memory estimate. Selecting the retained OpenAI API option displayed a prominent audio-upload disclosure and API key controls. No warning or error entries were captured in the final browser session. The preview uses the real settings/profile API and database, with transcription disabled; it is not the production deployment.

Authenticated API checks verified write-only global Hugging Face token storage, custom-profile token preservation on redacted edits, switching back to the saved default, and exact persistence of explicit false/zero values. Temporary test credentials were cleared, and no test token appeared in server logs. Admission tests cover token snapshots, authenticated-user scoping and immediate runs resolved from the stored profile ID. Pyannote telemetry is explicitly disabled before model imports; this was checked with dependency-free import guards and a source review, not a network capture.

## Independent regression review

The final review identified and corrected model-selector transitions that reset an external diarizer, inherited an invalid Whisper precision into Voxtral, or used Whisper precision for NVIDIA memory estimates. Focused transition tests preserve the selected speaker checkpoint and use the precision actually sent to the recognizer.

All three transitions also passed rendered browser checks against the actual API, including saved-profile readback: Whisper Int8 to Voxtral selected the exact checkpoint and CPU Float32; a Qwen variant change retained SUPlime's base checkpoint and CPU speaker processing; Canary's GPU estimate changed with its NVIDIA precision. No browser errors occurred.

Multipart and resumable submissions now retain the new chunk/checkpoint options and choose valid BitNet defaults. Invalid context or options are rejected before audio is moved into persistent job storage. Actual handler tests verify a client error, no saved audio and released upload reservations; resumable tests verify the assembled input remains available for retry or cancellation.

Qwen forced alignment now retains the original recognized spelling and punctuation when mapping timing onto words, including technical tokens such as `C++` and `foo.bar()`. A fresh CPU alignment run against the saved public-sample Qwen and BitNet transcripts returned 37 words for each and reproduced both original transcripts exactly after whitespace normalization. This verifies preservation of recognition output, not its correctness against the audio. Incomplete alignment mappings fail explicitly. External speaker assignment requires word alignment or usable native timing, and waiting for the BitNet runtime preparation lock is cancelable.

Converted audio is created beneath the job's temporary output directory so the Go worker can remove it even when Python is killed during conversion. A real subprocess cancellation test verifies removal of that converted audio and preservation of the original input; Python tests verify the conversion directories use the supplied job parent.

After the final processor-argument and temporary-directory fixes, Qwen3-ASR 0.6B completed a fresh CPU FP32 recognition-and-alignment run on the same public sample. It produced 35 aligned words, preserved the complete recognized surface text after whitespace normalization, and left no converted-audio directories in its output directory. This used cached models in an isolated container without network or GPU access.

The release Go vulnerability check reported no reachable vulnerabilities. It also reported three advisories in required modules outside the imported/reachable code; this is not a claim that every transitive module is free of advisories.

## GPU estimates, selection defaults and Auto recovery

CPU RAM and expected GPU VRAM were verified together in the actual model picker and selected details. The GPU-memory sort and multiline options were rendered at desktop size and a 390 × 844 mobile viewport. Browser interaction checks verified Qwen CPU → GPU selects FP16 and batch 1, switching to Canary preserves GPU with 40-second chunking and CPU speakers, and switching to Whisper preserves a manually entered profile name. Saving that GPU Whisper example and reading the actual database confirmed CUDA FP16, batch 1, CPU speakers and inherited token/context; the saved default profile was retained.

Auto recovery tests use deterministic simulated CUDA failures, not forced errors on physical GPU hardware. Go tests cover CUDA-to-CPU success, both attempts failing, CPU-only success, cancellation boundaries, stale-log rejection, explicit GPU remaining explicit, and speaker Same following the resolved ASR device. Multi-track processing re-enters the same per-track unified path. Python guards cover actual execution scopes, OOM/kernel/dtype failures, authentication/input exclusions, and CPU-only Auto precision normalization. The worker exits and cleans its temporary output before another attempt; successful result publication stays outside the retry loop. Run metadata and controlled log entries record successful CPU recovery.

The review also corrected unified routing of a selected legacy Pyannote 3.1 checkpoint for inline Whisper and standalone speaker processing. Direct-adapter checkpoint smoke tests above remain valid; focused routing tests now cover the user-facing checkpoint field. Granite Plus has no unused-aligner memory card, legacy Voxtral keeps its Qwen alignment estimate, and Parakeet's GPU estimate reflects its fixed FP32 weights with TF32 math.

## Remaining validation

- No meeting WER, speaker-attributed WER or diarization error rate was measured for this integration. The public smoke sample is not a representative software engineering meeting.
- GPU inference has not been exercised by this validation record.
- Long meetings, sustained memory usage and chunk-boundary recognition quality still need representative audio checks.
