# Reading the model comparison

The model selector displays published English word error rates (WER) and memory planning ranges. They help choose candidates to test; they do not measure the accuracy of your recordings. Lower WER is better. A model can score better on meeting clips and worse on a different conversational test.

The [2026-09-11 source snapshot](model-benchmarks-2026-09-11.json) records the exact relevant model rows from the [Hugging Face Open ASR Leaderboard](https://huggingface.co/spaces/hf-audio/open_asr_leaderboard), the source URLs and response hashes. AMI-Cleaned comes from `english_short_latest.csv`; private conversational WER comes from component 41 of the published Gradio configuration. Dates use America/Vancouver; retrieval and the local smoke measurement occurred on September 12 UTC. The public table's average and private table's average use different datasets and are not substituted for either displayed metric.

Results apply to exact checkpoints. The shared leaderboard's Whisper statistics apply only to `large-v3`; the other Whisper sizes have their own publisher and community evaluations. Granite 4.1 Plus does not inherit the base model's WER, and quantized/streaming VibeVoice variants do not inherit the full model's WER. AutoArk-AI/ARK-ASR-3B is a verified repository redirect to the pinned Edge0 checkpoint. A missing result means **no comparable result for the selected test**, not that a model has never been evaluated.

The picker defaults to **Recommended for meetings**, an editorial starting order based on published meeting/conversational results and meeting features. Cohere, Granite 4.1 and Qwen3 1.7B are the first candidates. Each recommendation has a short reason; placements across different evaluation setups are provisional. This is not a measured ranking on your recordings. Alternative sorts use lowest published **AMI meeting WER**, conversational WER, estimated CPU RAM or estimated GPU VRAM, with missing results last. Cloud APIs are listed after local models and do not inherit downloadable Whisper's scores or memory estimates.

The **Other published evaluations** section includes exact-checkpoint results from [OpenAI's Whisper paper, Table 9](https://cdn.openai.com/papers/whisper.pdf#page=22), [Superwhisper's app evaluations](https://superwhisper.com/benchmarks/whisper-small), [IBM's Granite Plus card](https://huggingface.co/ibm-granite/granite-speech-4.1-2b-plus#evaluations), [Microsoft's BitNet card](https://huggingface.co/microsoft/VibeVoice-ASR-BitNet#accuracy-wer), and the evaluator's own [transcribe.cpp](https://github.com/handy-computer/transcribe.cpp/blob/main/docs/models/granite-speech-4.1-2b-plus.md) and [DictatorFlow](https://dictatorflow.com/blog/vibevoice-bitnet-local-cpu-asr/) reports. Each score includes its dataset, publisher/community provenance, source and method caveats. The [31 supplemental records](../internal/transcription/additional_benchmarks.json) were retrieved on September 12, 2026. Different microphone setups, normalization and decoding can produce very different WER values for the same checkpoint, so these are not substituted into the common leaderboard sort.

## Local and cloud processing

Each transcription choice is labeled **Local** or **Cloud**. Local means audio recognition runs on the Scriberr server, using its CPU or GPU. Downloadable OpenAI Whisper runs locally through WhisperX/faster-whisper; the separate **OpenAI Whisper API** uploads audio and any recognition prompt to `https://api.openai.com/v1/audio/transcriptions`. The cloud option remains available and displays an upload notice when selected.

Local models may contact their model repositories to download weights and supporting files. A Hugging Face token authorizes those downloads; it does not make local transcription a hosted inference service. Pyannote recording-metadata telemetry is explicitly disabled before ML imports in the standalone Pyannote, WhisperX and research diarization runners.

Summaries and chat have an independent provider setting. OpenAI sends transcript text and prompts to its cloud endpoint by default, or to a configured compatible endpoint. Ollama sends them to its configured URL; use your own server to keep that processing local. Choosing a local transcription model does not change this separate setting.

## Speaker accuracy

WER counts substituted, deleted and inserted words relative to a reference transcript. Diarization error rate (DER) measures speaker timing/attribution, including missed speech and false alarms. Adding WER and DER does **not** yield an effective WER: they measure different errors over different denominators and may use different datasets. End-to-end speaker-attributed transcription needs a manually checked transcript with speaker labels, a defined text-normalization policy, and a metric such as speaker-attributed WER or permutation-invariant cpWER.

The Sortformer 2.1 result is the publisher's AMI Test SDM DER with a 30.4-second input buffer, at most four speakers, overlap included, zero collar and forced-alignment-derived reference labels. Community-1 shows its publisher's 19.9% AMI SDM result, only for that checkpoint. Supplemental records cover [Pyannote 3.1](https://huggingface.co/pyannote/speaker-diarization-3.1#benchmark), [DiariZen v2](https://github.com/BUTSpeechFIT/DiariZen#benchmark), [SUPlime](https://huggingface.co/rewayai/suplime#results), [SUPlime-L](https://huggingface.co/rewayai/suplime-large#results), and the SUPlime authors' independent DiariZen rerun. These retain their own microphone/reference protocol and publisher runtime conditions. SUPlime's published CUDA FP16 results are not this integration's CPU accuracy measurements; SUPlime-L's batching setting can affect its output. None is asserted to be a controlled comparison with Sortformer's reference labels.

## Memory

Displayed GB means decimal gigabytes (1 GB = 1,000,000,000 bytes). Legacy `memory_mb` capability allowances use MiB and are converted before display. CPU estimates use the adapter allowance through 1.5 times that allowance as a rough working range for one FP32 worker with short chunks. Whisper estimates are checkpoint-specific, using the [published parameter counts](https://github.com/openai/whisper#available-models-and-languages): four bytes per parameter plus 2–6 GB runtime headroom for CPU FP32, rounded upward, with a separate 10–16 GB allowance for full-size Whisper. These are planning estimates, not measured peaks; alignment and diarization can require more memory.

GPU ranges are unmeasured planning estimates: approximate parameter count times 2 bytes (FP16/BF16) or 4 bytes (FP32), plus 2–6 GB for runtime overhead with batch size 1 and short chunks. The picker shows CPU RAM and prospective GPU VRAM together. The selected model details use the actual GPU precision when GPU/Auto is selected; CPU mode also shows the default GPU alternative for comparison. Fixed-precision runtimes take precedence: Parakeet keeps FP32 weights and enables TF32 CUDA math, so it has no FP16 estimate. Parameter counts include the audio components where the leaderboard reports them; model marketing names can describe only the decoder. Quantized runtimes need their own measurements rather than these full-precision estimates.

Qwen3-ASR 1.7B is the exception with a local smoke measurement: a public 15-second sample on a Ryzen 7 5700G using CPU FP32 and word alignment reached 12,878,776 KiB child peak RSS (about 12.3 GiB / 13.2 GB). Its displayed 13–17 GB planning range is not a prediction for an hour-long meeting. The Voxtral 3B card separately reports about 9.5 GB GPU RAM for FP16/BF16; that is publisher guidance, not a measured peak for this integration.

VibeVoice BitNet instead shows a 9–13 GB unmeasured allowance for quantized ASR followed by a separate FP32 aligner; its CPU-only fixed-precision runtime is labeled explicitly.

Diarizer estimates are separate per-checkpoint, unmeasured planning allowances that include runtime headroom. SUPlime uses FP32 weights with FP16 CUDA encoder autocast; that range is not presented as full FP16 execution. Qwen alignment has its own estimates, while language-dependent WhisperX alignment remains unknown. The selected configuration lists the device and memory of each stage rather than adding sequential stages into a fictitious simultaneous peak.

Loading can temporarily increase memory. Full-recording context, larger batches, other GPU processes, alignment and concurrent jobs can exceed the ranges. MOSS native diarization uses a full-recording default to preserve speaker identities, so the short-chunk assumption is especially limited there. CPU execution alone does not imply greater accuracy than GPU execution at equivalent precision and decoding settings.

## Auto device recovery

**Auto · GPU, then CPU** uses an available GPU first and retries once on CPU after a confirmed GPU execution failure. CPU retry uses FP32 where the runtime supports floating precision. A CPU-only host goes directly to CPU. Explicit GPU selection remains explicit; missing model access, invalid input, cancellation and expired job deadlines are not converted into another attempt.

The failed adapter process exits before CPU retry, and the job publishes a transcript only after processing succeeds. Run logs record the attempts; successful recovery is also shown under Runs → Devices used. If both attempts fail, the job reports that CPU fallback failed after the GPU failure. Each stage gets its own recovery: **Same as transcription** follows the actual ASR device after fallback, while an independently selected speaker **Auto** can try GPU and then CPU itself. Both attempts share the original job deadline.

## Long CPU jobs

`MEDIA_PROCESS_TIMEOUT_MINUTES` already controls the queued transcription deadline and media subprocess limits. It defaults to **120 minutes** and accepts **5–1440 minutes**; for example, `MEDIA_PROCESS_TIMEOUT_MINUTES=1440` allows up to 24 hours. Set the server environment before starting a long CPU run. The transcription deadline includes first-use installation and model loading, recognition, alignment and external diarization, so leave time for the complete pipeline. Increasing this setting also extends media subprocess limits.

## Profile starters and Quick Add Presets

**Create New Profile** opens an editable CPU starter using Qwen3-ASR 1.7B, FP32, batch size 1 and Community-1 diarization on CPU. It inherits the user's saved context, vocabulary and Hugging Face token. Opening the editor does not create a profile.

**Quick Add Presets** offers nine reference configurations and three suggested alternatives. The reference hardware is a Ryzen 7 5700G, 64 GB installed RAM with 32 GB available for models, and RTX 3060 12 GB. The reference set reproduces 459 captured technical values from nine saved profiles, with private credentials, prompts and paths excluded. Each user's token and context are inherited. Legacy automatic speaker-device behavior and a GPU-named Parakeet profile actually saved with CPU transcription are flagged explicitly.

Explicit model or device changes apply compatible execution defaults: batch size 1, CPU FP32 or GPU/Auto FP16 where supported (Auto CPU execution uses FP32), model-specific chunking/token limits, and native or external speaker settings. The chosen CPU/GPU device is retained when the new model supports it. Canary GPU selections use 40-second chunks and CPU external speaker processing for the 12 GB reference setup. Fixed CPU-only models return to CPU. Opening a saved/reference profile preserves its stored values; only explicit picker changes apply new defaults. Untouched new-draft names follow the model/device selection; manually entered and existing-profile names are preserved.

Users can select profiles to add immediately or edit one first. Existing names are skipped, existing profiles and an explicitly saved default are preserved, and the list refreshes after a partial failure so retrying does not blindly duplicate successful additions. These presets are starting points; the listed hardware does not guarantee the same memory use for every recording.

## Context and vocabulary

Settings → Transcription stores default meeting context and one vocabulary term per line. New jobs inherit those defaults unless a profile or individual job overrides them. An empty override explicitly disables that hint. Engineering hints should name the actual systems and terminology (for example, Kubernetes, gRPC and a project name), rather than ask the recognizer to invent or rewrite content.

A useful starting context is:

> Software engineering meeting about code architecture, code quality, code reviews, implementation, testing, and deployment.

Add the actual project and service names being discussed. Put exact spellings of uncommon names, libraries and acronyms in the separate vocabulary box, one per line.

Only supported hints are forwarded: some models accept prose plus vocabulary, some accept vocabulary alone, and others accept neither. Context can improve uncommon names, but can also bias recognition; compare the same representative excerpt with and without hints before adopting a default.

## Hugging Face access

Accept the access conditions using the account associated with the worker's Hugging Face token before selecting these checkpoints:

- [Cohere Transcribe](https://huggingface.co/CohereLabs/cohere-transcribe-03-2026).
- [Pyannote Community-1](https://huggingface.co/pyannote/speaker-diarization-community-1).
- For the supported legacy pipeline, [Pyannote speaker diarization 3.1](https://huggingface.co/pyannote/speaker-diarization-3.1) and [segmentation 3.0](https://huggingface.co/pyannote/segmentation-3.0).

Save a read token once in **Settings → Transcription → Hugging Face token**. Profiles and individual runs inherit the signed-in user's saved token by default; a custom token can override it for one profile/run. The saved value is write-only: responses expose only whether a token is configured. Choosing the server fallback skips the saved user default and leaves the worker's `HF_TOKEN` environment variable or cached Hugging Face login available.

The selected credential is captured when a run is admitted, so changing Settings does not change an already queued run. API-key requests without a signed-in user do not borrow another user's default. The other shortlisted checkpoints do not require an access request, but their displayed licenses still apply. Optional model runtimes and weights install on first selection rather than application startup.
