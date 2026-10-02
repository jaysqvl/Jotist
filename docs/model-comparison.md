# Reading the model comparison

The model selector displays published English word error rates (WER) and memory planning ranges. They help choose candidates to test; they do not measure the accuracy of your recordings. Lower WER is better. A model can score better on meeting clips and worse on a different conversational test.

The [2026-09-11 source snapshot](model-benchmarks-2026-09-11.json) records the exact relevant model rows from the [Hugging Face Open ASR Leaderboard](https://huggingface.co/spaces/hf-audio/open_asr_leaderboard), the source URLs and response hashes. AMI-Cleaned comes from `english_short_latest.csv`; private conversational WER comes from component 41 of the published Gradio configuration. Dates use America/Vancouver; retrieval and the local smoke measurement occurred on September 12 UTC. The public table's average and private table's average use different datasets and are not substituted for either displayed metric.

Results apply to exact checkpoints. The shared leaderboard's Whisper statistics apply only to `large-v3`; the other Whisper sizes have their own publisher and community evaluations. Granite 4.1 Plus does not inherit the base model's WER, and quantized/streaming VibeVoice variants do not inherit the full model's WER. AutoArk-AI/ARK-ASR-3B is a verified repository redirect to the pinned Edge0 checkpoint. A missing result means **no comparable result for the selected test**, not that a model has never been evaluated.

The picker defaults to an **English meeting shortlist**, sorted by **Recommended for English meetings**. Its eight exact checkpoints cover different needs: Cohere Transcribe, Qwen3-ASR 1.7B and Granite 4.1 2B for accuracy; Canary-Qwen 2.5B as another English accuracy candidate; Parakeet TDT 0.6B v3 for GPU efficiency; Qwen3-ASR 0.6B and Apache-licensed Granite 5.0 470M TurboCTC as smaller CPU candidates; and Whisper large-v3 as an established baseline. This editorial order considers published English results, features and resource requirements. It does not qualify speed, memory fit or reliability on your recordings.

**All models** retains the supported catalog, including translation, specialist, older Whisper and cloud options. The current saved selection stays visible even outside the shortlist. Non-English and translation configurations open the full list. Opening a profile preserves its language and all stored execution settings; an explicit model change from the English shortlist selects English. Exact-checkpoint shortlist membership and recommendation reasons come from shared server metadata. Older servers without this metadata continue to show the full catalog.

Alternative sorts use lowest published **AMI meeting WER**, conversational WER, estimated CPU RAM or estimated GPU VRAM, with missing results last. Results from different evaluation setups are provisional comparisons. Cloud APIs are listed after local models and do not inherit downloadable Whisper's scores or memory estimates.

The **Other published evaluations** section includes exact-checkpoint results from [OpenAI's Whisper paper, Table 9](https://cdn.openai.com/papers/whisper.pdf#page=22), [Superwhisper's app evaluations](https://superwhisper.com/benchmarks/whisper-small), [IBM's Granite Plus card](https://huggingface.co/ibm-granite/granite-speech-4.1-2b-plus#evaluations), [Microsoft's BitNet card](https://huggingface.co/microsoft/VibeVoice-ASR-BitNet#accuracy-wer), and the evaluator's own [transcribe.cpp](https://github.com/handy-computer/transcribe.cpp/blob/main/docs/models/granite-speech-4.1-2b-plus.md) and [DictatorFlow](https://dictatorflow.com/blog/vibevoice-bitnet-local-cpu-asr/) reports. Each score includes its dataset, publisher/community provenance, source and method caveats. The [31 supplemental records](../internal/transcription/additional_benchmarks.json) were retrieved on September 12, 2026. Different microphone setups, normalization and decoding can produce very different WER values for the same checkpoint, so these are not substituted into the common leaderboard sort.

## Multilingual coverage and accuracy

The **Compare models by language** table uses the [pinned HF multilingual CSVs](https://huggingface.co/datasets/hf-audio/multilingual_evals/tree/d2341ed252c0bc3f692b4dd02839f41d96673c3b) retrieved on **2026-09-28**. Its default common comparison uses German, French, Italian, Spanish, Portuguese and Dutch. The six-language macro WER is the mean of six per-language means; each language mean first averages its available datasets and rounds to two decimals, following the [evaluator](https://huggingface.co/spaces/hf-audio/open_asr_leaderboard/blob/4295ce2e317d85022f232f5c12a1f10586b41ca7/app.py). A row missing any selected language receives no aggregate. The table can also compare one language or those six plus Hindi.

| Exact checkpoint | Publisher ASR coverage | Jotist language choices | English AMI WER | Six European WER | Hindi Monsoon WER |
| --- | --- | --- | ---: | ---: | ---: |
| [Cohere Transcribe 2B](https://huggingface.co/CohereLabs/cohere-transcribe-03-2026) | 14 languages | 14 | 7.02% | 3.84% | — |
| [Whisper large-v3](https://huggingface.co/openai/whisper-large-v3) | 100 languages | 99 + auto | 13.63% | 4.57% | 28.17% |
| [Canary 1B v2](https://huggingface.co/nvidia/canary-1b-v2) | 25 European languages | 8 | 13.03% | 4.97% | — |
| [Voxtral Mini 3B](https://huggingface.co/mistralai/Voxtral-Mini-3B-2507) | 8 languages | 8 | 13.57% | 5.20% | 22.90% |
| [Parakeet TDT 0.6B v3](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3) | 25 European languages | English | 9.42% | 5.25% | — |
| [Qwen3-ASR 1.7B HF](https://huggingface.co/Qwen/Qwen3-ASR-1.7B-hf) | 30 languages + 22 Chinese dialects | 12 | 8.31% | 5.52% | 12.25% |
| [Qwen3-ASR 0.6B HF](https://huggingface.co/Qwen/Qwen3-ASR-0.6B-hf) | 30 languages + 22 Chinese dialects | 12 | 9.33% | 9.01% | 18.13% |
| [MOSS Transcribe Diarize 0.9B](https://huggingface.co/OpenMOSS-Team/MOSS-Transcribe-Diarize) | 50+ languages (publisher claim) | 15 | 8.33% | 9.08% | — |
| [ARK ASR 3B](https://huggingface.co/Edge0/ARK-ASR-3B) | 19 languages | 19 | 7.89% | — | — |
| [Canary-Qwen 2.5B](https://huggingface.co/nvidia/canary-qwen-2.5b) | English only | English | 7.91% | — | — |
| [Granite 5.0 470M TurboCTC](https://huggingface.co/ibm-granite/granite-speech-5.0-470m-turboctc) | English only | English | 7.72% | — | — |
| [Granite 5.0 470M TurboCTC NC](https://huggingface.co/ibm-granite/granite-speech-5.0-470m-turboctc-nc) | English only | English | 7.13% | — | — |
| [Granite Speech 4.1 2B](https://huggingface.co/ibm-granite/granite-speech-4.1-2b) | 6 languages | 6 | 7.06% | — | — |
| [Granite Speech 4.1 2B Plus](https://huggingface.co/ibm-granite/granite-speech-4.1-2b-plus) | 5 languages | 5 | — | — | — |
| [MOSS Preview 2B](https://huggingface.co/OpenMOSS-Team/MOSS-Transcribe-preview-2B) | English only | English | 7.80% | — | — |
| [VibeVoice ASR BitNet](https://huggingface.co/microsoft/VibeVoice-ASR-BitNet) | 7 named languages, plus others claimed | Not enumerated | — | — | — |

English AMI is the separate **2026-09-11** English snapshot described above. It is not included in either multilingual aggregate. German uses FLEURS/MCV; French, Italian, Spanish and Dutch use FLEURS/MCV/MLS; Portuguese uses FLEURS/MLS. These European tests largely contain read speech. Hindi uses the different conversational Monsoon protocol, including orthographic-variant scoring. The seven-language view combines those different audio domains; it is not a score over every language a model supports.

### Per-language results

| Exact checkpoint | German | French | Italian | Spanish | Portuguese | Dutch | Hindi |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Cohere Transcribe 2B | 3.10% | 4.02% | 2.97% | 2.81% | 5.99% | 4.17% | — |
| Whisper large-v3 | 4.00% | 6.24% | 4.45% | 3.35% | 4.94% | 4.42% | 28.17% |
| Canary 1B v2 | 4.06% | 4.79% | 4.75% | 3.23% | 6.26% | 6.73% | — |
| Voxtral Mini 3B | 4.46% | 5.96% | 5.12% | 4.16% | 5.08% | 6.39% | 22.90% |
| Parakeet TDT 0.6B v3 | 4.12% | 5.38% | 4.66% | 3.71% | 6.08% | 7.53% | — |
| Qwen3-ASR 1.7B HF | 3.97% | 5.68% | 5.44% | 3.80% | 6.31% | 7.95% | 12.25% |
| Qwen3-ASR 0.6B HF | 6.69% | 8.76% | 9.09% | 5.82% | 10.12% | 13.57% | 18.13% |
| MOSS Transcribe Diarize 0.9B | 8.02% | 10.66% | 8.47% | 5.50% | 7.28% | 14.53% | — |

These are recognition WER values. They do not measure Jotist's CPU/GPU speed, complete-recording coverage, word alignment, diarization or mixed-language meetings.

### What the comparison supports

- **Cohere** leads this six-language comparison; **Whisper large-v3** is next and has much broader publisher coverage. Cohere expects one selected language and does not promise reliable code switching.
- **Canary v2** scores better than **Parakeet v3** on this European comparison and adds speech translation. Parakeet remains a distinct efficiency candidate. Their published results do not establish speed or reliability on a shared media server.
- **Qwen3-ASR 1.7B** leads the four checkpoints with all seven measured languages: 6.49%, versus Voxtral 3B 7.72%, Whisper large-v3 7.94%, and Qwen 0.6B 10.31%. Much of that ordering comes from Hindi; it does not rank Cohere, Canary or Parakeet, which lack Hindi rows here.
- **Voxtral Mini 3B** remains supported: its multilingual behavior differs from the retired **Voxtral Mini 4B Realtime**. Realtime is removed from the supported catalog and execution branch; historical run labels, presentation support and dated validation snapshots remain readable. Saved Realtime selections are shown as unavailable and are not silently replaced with 3B.
- **MOSS Transcribe Diarize** has weaker WER on these six languages but adds native speaker labels and timing. That feature cannot be judged from recognition WER alone.
- **Granite 4.1 base/Plus, ARK and VibeVoice BitNet** have published multilingual coverage but no exact-checkpoint result in this common snapshot. The Granite NAR and full VibeVoice rows are different checkpoints and are not borrowed. BitNet has its own publisher evaluations in Other published evaluations.
- **Canary-Qwen, Granite 5.0 TurboCTC and MOSS Preview** are English-only checkpoints. They contribute English options; their encoder or family name does not confer multilingual support.

### Integration coverage still needs qualification

Publisher language counts and Jotist language choices describe different things. Parakeet v3 is multilingual upstream, but the current Jotist runner advertises and labels output as English. Canary v2's older integration offered Hindi, Japanese, Korean and Chinese even though this exact checkpoint does not support them; those choices are now removed from the draft UI/schema/CLI. The eight previously offered supported languages remain, while expanding to all 25 awaits qualification.

Qwen's publisher covers 30 languages plus Chinese dialects; Jotist currently offers 12. Its default forced aligner publishes only 11 languages (Chinese, English, Cantonese, French, German, Italian, Japanese, Korean, Portuguese, Russian and Spanish), so an Arabic or Hindi recognition result does not qualify the default complete pipeline. MOSS exposes 15 of its claimed 50+ languages. Whisper large-v3 has 100 published language tokens; Jotist currently lists 99 language codes plus auto detection. VibeVoice BitNet's unrestricted language field means no enumerated restriction, not tested support for every language.

Asian-language publisher scores can use character error rate (CER), different datasets and different language sets. They are not merged into this WER ranking. The [shared comparison records](../internal/transcription/language_comparison.json) preserve exact checkpoints, publisher sources, raw per-dataset values, language means, the evaluator revision and retrieval date. The server supplies that same data to the picker; missing data stays unranked.

## Local and cloud processing

Each transcription choice is labeled **Local** or **Cloud**. Local means audio recognition runs on the Jotist server, using its CPU or GPU. Downloadable OpenAI Whisper runs locally through WhisperX/faster-whisper; the separate **OpenAI Whisper API** uploads audio and any recognition prompt to `https://api.openai.com/v1/audio/transcriptions`. The cloud option remains available and displays an upload notice when selected.

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

The failed adapter process exits before CPU retry, and the job publishes a transcript only after processing succeeds. Run logs record the attempts; successful recovery is also shown under Runs → Devices used. If both attempts fail, the job reports that CPU fallback failed after the GPU failure. Each stage gets its own recovery: **Same as transcription** follows the actual ASR device after fallback, while an independently selected speaker **Auto** can try GPU and then CPU itself. Both attempts share the saved attempt budget and any explicitly supplied execution deadline.

## Long CPU jobs

Queued and quick transcription have **no default wall-clock deadline**. A healthy CPU worker can continue beyond two hours. Active quick jobs retain their audio; their six-hour cleanup period starts when processing ends.

All model workers use the same inactivity supervisor. `TRANSCRIPTION_STALL_MINUTES` defaults to **30 minutes** and accepts **0–1440**; `0` disables automatic inactivity termination. On Linux, advancing progress counters, descendant CPU or disk activity, runnable workers, blocked I/O and scheduler contention keep a worker alive. A sleeping GPU worker is stopped only when GPU idleness is confirmed. Missing activity or GPU readings defer termination. Repeated heartbeats alone do not count as progress.

This conservative policy detects sustained observable inactivity, not every possible hang: a busy loop, ongoing background I/O or an unverified GPU can defer termination. Cancellation still stops the worker process group and retained checkpoints remain available. Explicit caller deadlines and deadlines saved by older executions remain enforced on those executions; a new ordinary run has no such deadline.

`MEDIA_PROCESS_TIMEOUT_MINUTES` continues to control download/conversion subprocess limits (**120 minutes**, range **5–1440**), independently of inference. Model setup downloads and resource admission have their own bounded waits. GPU and stage capacity waits currently allow ten minutes; these are admission limits before inference, not transcription duration limits. See [shared lifecycle and release checks](recoverable-transcription.md#shared-worker-lifecycle-and-release-checks).

## Profile starters and Quick Add Presets

**Create New Profile** opens an editable CPU starter using Qwen3-ASR 1.7B, FP32, batch size 1 and Community-1 diarization on CPU. It inherits the user's saved context, vocabulary and Hugging Face token. Opening the editor does not create a profile.

**Quick Add Presets** offers nine reference configurations and five English starting points: Cohere and Qwen3-ASR 1.7B on CPU, Parakeet TDT 0.6B v3 on GPU with CPU speakers, and the smaller Qwen3-ASR 0.6B and Granite 5.0 470M TurboCTC on CPU. Each uses batch size 1, English and Community-1 speakers on CPU. New starters use the same model-selection defaults as the picker, including Parakeet's fixed FP32 weights. They still require full-recording runtime qualification.

The reference hardware is a Ryzen 7 5700G, 64 GB installed RAM with 32 GB available for models, and RTX 3060 12 GB. The reference set reproduces 459 captured technical values from nine saved profiles, with private credentials, prompts and paths excluded. Each user's token and context are inherited. Legacy automatic speaker-device behavior and a GPU-named Parakeet profile actually saved with CPU transcription are flagged explicitly.

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
