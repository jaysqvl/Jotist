# Profile qualification and run evidence

Status: release candidate, 2026-09-28. The operator closed the original benchmark by cancelling its remaining CPU work: **20 completed, 17 failed, 3 cancelled**. Production remains on v1.7.6 during focused qualification. Implementation, contract checks, real inference and human quality assessment are distinct evidence; no single check clears every model.

## Release docket

- [x] **Readable run selection.** Searchable run and comparison pickers show saved profile, exact model/diarizer, requested and reported CPU/GPU devices, precision, elapsed time and the pinned transcript. Filters distinguish ordinary completion, completion with recovery and failure. A failed fallback remains failed; deliberately different recognition/speaker devices do not imply fallback.
- [x] **Shared resource measurements.** Run/stage details show worker descendant RAM and CPU, whole-device and owned VRAM, reserve, measurement availability, retries, actual attempts and checkpoint reuse. Unknown readings stay unknown. Numeric measurements survive cancellation without bypassing write ownership.
- [x] **Remove default execution duration caps.** Queue and quick jobs have no default two-hour deadline; recoverable execution has no hidden twenty-four-hour default. Explicit caller and historical saved deadlines remain enforced. Active quick audio is retained and its cleanup period starts at completion.
- [x] **Use one conservative activity supervisor.** Shared process execution covers setup, recognition, alignment and speakers. Monotonic progress, descendant CPU/disk activity, runnable work and scheduling/I/O contention keep work alive. Missing telemetry defers termination. A sleeping GPU worker also requires confirmed device idleness. The default is thirty idle minutes. This detects sustained observable inactivity, not every busy-loop hang.
- [x] **Shared recognition-stage boundary.** Clone the final request and disable later alignment/external-speaker requirements during recognition. Retain native speaker labels. This fixes BitNet's pre-inference rejection of a saved final request requiring aligned external speakers.
- [x] **SQLite writer contention.** Apply a busy timeout on every pooled connection, reserve the writer before reading queue positions and retry only rolled-back transient lock failures. A real second-connection regression passes.
- [x] **English selection and language comparison.** Default to eight English checkpoints while retaining the full catalog and saved specialist selections. Shared comparison metadata distinguishes exact variants, publisher language coverage and available per-language results. Incomplete sets do not acquire fabricated rankings. Retire Mini 4B Realtime while retaining Mini 3B and historical run labels.
- [ ] **Complete focused target inference.** Qualify Canary's blank aligned-row normalization, DiariZen's batch/typed-memory propagation, MOSS terminal timing recovery and Granite PLUS's bounded invalid-timestamp retry. Fixtures pass; target evidence below records each exercised path separately.
- [x] **Private comparison and profile export.** Read the closed cohort without changing its queue/history. Back up all forty profiles and their revisions outside the public repository. Derive counts, timing, vocabulary match counts, repetition and peer token agreement without exposing meeting text or hints. Agreement is not accuracy, and unreviewed trailing audio remains unknown.
- [ ] **Publish, deploy and finish cleanup.** Merge the validated source, publish its release and deploy an immutable image digest. Preserve rollback identity, bound Docker log rotation, check audio/history/baseline pin, retire only the two explicitly selected Realtime profiles and remove qualification scratch.

The review is [PR #25](https://github.com/jaysqvl/Jotist/pull/25). Changes run through the release workflow; local source checks do not imply deployment.

## Failure disposition

| Observed failure | Prepared response | Qualification boundary |
| --- | --- | --- |
| Eleven CPU runs reach the old two-hour deadline, mostly during speakers | Remove ordinary execution duration defaults; shared conservative activity supervision | Each model still needs full-recording speed/output evidence |
| Canary checkpoint rejects blank aligned words | Skip blank separators, retain valid words and strict time validation | Target staged recognition/alignment |
| DiariZen wraps CUDA OOM in MemoryError; saved batch not forwarded consistently | Follow typed exception causes; forward batch to segmentation and embedding | Target CPU/GPU inference and resource behavior |
| MOSS returns complete turns followed by one terminal turn without its end timestamp | After natural EOS, retain terminal text/speaker and use the existing aligner for its timing after unloading recognition | Repaired full-recording replay; no guessed end or partial publication |
| Granite PLUS predicts native timing outside a recognition window | Retain strict validation; reuse a quiet-boundary split once for the offending window on the same model/device | Full-recording CPU replay |
| BitNet recognition inherits final speaker requirements | Common recognition-only parameter projection | Staged CPU fixture passed; full meeting remains unqualified |
| SQLite queue insertion collides with a writer | Per-connection busy timeout, writer reservation and bounded transient-lock retry | Real cross-connection regression passed |

Native timing recovery uses the same bounded window machinery for recognizers without native speaker identities. MOSS retains recording-scoped native speaker IDs. Malformed middle output, generation cutoff, unsupported alignment language or an invalid terminal audio range remain explicit failures. Decoder/window retries and output repairs appear as recovery in run selection and details.

## Current checks

- 104 frontend tests pass. Type checking, ESLint and the production Vite build pass. Desktop and narrow rendered fixtures were reviewed with synthetic data.
- Affected Go API, queue, database, repository, transcription, adapter and process packages pass; go vet passes. Ownership, cancellation, explicit deadlines, deletion and checkpoint regression coverage remains intact.
- Shared Python contracts pass **114 tests and 28 subtests**. Script tests validate the pinned fixture and actual-inference gate invocation.
- Actual Linux process tests on the target exercise idle descendant termination, CPU activity, monotonic progress and cancellation. Recovery fixtures allow resuming an execution started more than two hours earlier without a default deadline and protect active quick audio from expiry.
- Production model subprocesses and audio metadata probes use the shared supervised launcher. Direct process constructors in transcription/queue code are bounded GPU telemetry queries.
- The CI installed-import workflow now also requires real staged Whisper CPU recognition/alignment on a revision- and hash-pinned public speech fixture. Imports, audits and actual inference are separate checks.
- BitNet CPU recognition plus Qwen alignment completed the **16.02-second public fixture in 31.10 seconds**, producing 37 words with valid timing.
- A full MOSS GPU replay reproduced the native terminal omission after 538 complete turns. The terminal repair has fixture coverage; its repaired target replay is pending. Granite PLUS CPU is receiving a full-recording replay.
- Concurrent Canary model restoration caused host memory pressure and temporarily delayed health responses. Both qualification workers were stopped and health recovered. These interrupted checks are not passes. The opt-in harness now reserves one host slot across test binaries; heavy target checks run serially.

Target qualification uses isolated container temporary projects and installed cached runtimes. It does not edit production environments, recordings or queue items. No new forty-profile campaign is needed. Check for new production work before replacement, and remove only this task's temporary assets when done.

## Profile disposition and evidence rules

Only the two saved Mini 4B Realtime profiles are selected for retirement. Their immutable run history remains. Completed CPU profiles use different diarizers from their GPU counterparts; model identity alone does not make them redundant. The original GPU results have unknown process ownership, so they do not prove uncontended comfortable operation. Retain other CPU profiles until equivalent-stage evidence justifies removal.

The historical study used an RTX 3060 with 12 GiB nominal VRAM. Comfortable GPU use requires complete output, all enabled inference stages on CUDA, real time factor at most one, no retry/fallback/observed contention and at least 15% device reserve. Completion with lower reserve or recovery is fragile; unknown ownership or incomplete coverage is inconclusive. CPU interpretation likewise needs observed host reserve and contention evidence. Timestamp endpoints near 99.7% do not prove whether the remaining audio is silence or omitted speech. No WER or DER is claimed without a reviewed reference.

Private comparison statistics show token counts, punctuation, repeated four-token windows, timestamp validity, saved vocabulary term counts and token agreement with another model. Natural repetition, differing tokenization and legitimate punctuation can affect these values. They are review aids, not a deletion ranking or a human assessment of technical accuracy/speaker identity.

## Measurement limits

- Worker sampling is about every 500 ms. Peaks can miss short bursts and short-lived descendants. Summed RSS can count shared pages more than once.
- CPU uses 100% for one logical core; capacity comes from affinity/cgroup quota. Full host CPU contention is not measured by the resource collection change.
- Time averages are weighted by observed intervals. Missing observations break an interval; valid zero readings remain zero. Peaks across attempts are maxima.
- Whole-device VRAM includes unrelated applications. Owned VRAM needs matching process identity across Docker's PID namespace; unavailable ownership remains unknown. Reserve does not establish a safe batch-size increase.
- Reused stages do not fabricate inference cost. Old runs cannot acquire missing averages retroactively. Separate invocations preserve resume timing history.
- Run and stage collectors sample independently. Their GPU probes and process reads add overhead; no zero-overhead or all-model runtime guarantee is made.

See [shared lifecycle and release checks](../recoverable-transcription.md#shared-worker-lifecycle-and-release-checks) and [long CPU job policy](../model-comparison.md#long-cpu-jobs).
