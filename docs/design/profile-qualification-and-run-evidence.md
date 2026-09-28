# Profile qualification and run evidence

Status: implementation prepared locally, 2026-09-27. Production remains on
v1.7.6 while the existing meeting benchmark finishes. Checked tasks below mean
that implementation and local verification are complete. Hardware qualification
and meeting quality assessment remain separate gates; a completed process alone
is not proof of transcription quality or useful CPU/GPU performance.

## Ordered task list

- [x] **1. Replace vague dropdowns with a searchable run picker.** Include readable completed,
  failed, running, pending, cancelled, and interrupted labels in the selected
  value and options. Preserve access to failed runs, logs, and retained partial
  output. Show saved profile, model, diarizer, CPU/GPU, precision, elapsed time,
  and pinned transcript markers. Filter by outcome and device. Keep requested
  settings distinct from reported runtime and fallback devices. Verify the picker and comparison
  selector using local rendered fixtures. Completed with recovery has its own
  outcome filter and badge. A failed fallback remains failed. Auto resolving to
  CPU and explicitly configured GPU recognition with CPU speakers are ordinary
  completion, unless recorded attempts show retries or fallback.
- [x] **2. Inventory and extend resource measurement across adapters.** Record
  peak and time averaged owned process RAM, process CPU consumption, device
  VRAM, measurement duration/count, and ownership/contamination evidence. Keep
  requested settings, measured values, and unavailable data distinct. Include
  children of model workers; separate whole device VRAM from process VRAM and
  allocator peaks. Use one shared execution path for all model families. Local
  process, ownership, averaging, persistence, and cancellation fixtures pass;
  live sampling overhead and hardware qualification are pending.
- [x] **3. Present run and stage measurements in execution details.** Include
  total processing time, stage/attempt timing, retries, actual devices, RAM and
  VRAM usage, CPU convention, headroom, measurement availability, and checkpoint
  reuse. Do not infer a safe batch size from one memory sample. Do not report
  old runs' missing averages as zero. Desktop and narrow-screen rendered fixture
  review is complete; the screenshots use synthetic values.
- [x] **4. Fix the reproducible Canary aligned-word checkpoint failure.** Trace
  both failed profiles through adapter output and checkpoint validation. Add a
  local regression fixture based on the shape of the failure. Preserve valid
  text and timestamps without accepting corrupt output. Skip blank separator
  rows while retaining valid words and strict timestamp validation. Actual
  Canary inference with the fix is deferred.
- [x] **5. Fix DiariZen memory error classification and batching.** The saved
  GPU profile failed with a CUDA OOM wrapped in a Python MemoryError. Verify
  propagation of batch settings and typed memory failures so supported recovery
  can act. Retain the configured recovery policy and report actual attempts.
  Forward the saved batch size to both DiariZen segmentation and embedding;
  inspect nested CUDA memory failures without reclassifying unrelated errors.
  Actual CPU/GPU inference with these changes is deferred.
- [ ] **6. Address MOSS native long recording output.** The GPU run produced an
  incomplete timestamped result; its CPU counterpart reached the job deadline.
  Verify the native model's long audio/output contract and supported windows,
  token budgets, and speaker stitching before choosing a fix. Do not label the
  model unusable solely from one adapter failure.
- [ ] **7. Triage every additional terminal failure.** Separate adapter/format
  errors, memory failures, job deadlines, unsupported runtimes, and checkpoint
  persistence failures. Link each reproducible defect to a targeted regression
  and concrete disposition. Track the transient SQLite queue insertion lock.
- [ ] **8. Produce the private transcription comparison report.** Compare all
  completed outputs on the same recording: technical vocabulary, omissions,
  repetition, punctuation, timestamps, speaker attribution, elapsed time,
  stage cost, memory, and device evidence. Include CPU/GPU pairs and clear
  limitations. Agreement with another model is not reference accuracy; do not
  claim WER/DER without a reviewed reference. Keep meeting text, context,
  vocabulary, credentials, and private artifacts outside the public repository.
- [ ] **9. Review profile cleanup after the batch ends.**
  List affected names, devices, completion times, output quality, exact failures,
  and retain/remove/repair rationale first. Recommend a GPU-only failure for
  removal only when the CPU counterpart actually completes and is useful.
  Recommend a redundant CPU profile only when the corresponding GPU profile
  comfortably completes the full meeting with acceptable output quality.
  Different diarizers are separate profile choices. Preserve a private profile
  export before any separately authorized cleanup; benchmark IDs and history
  must stay intact. This preparation phase does not change saved profiles.
- [ ] **10. Prepare GitHub changes and report the stopping point.** Commit
  implementation and regression evidence in reviewable draft PRs. Record which
  checks are local, which require later hardware qualification, and which tasks
  await the running batch. Do not merge, publish a release, or replace the live
  container during this preparation phase.

## Validation boundary

The current benchmark has priority. The NAS receives brief, indexed read-only
status queries only. Do not add inference work, copy large live data trees,
perform database integrity scans, change profile settings, restart containers,
cancel/requeue items, or deploy while it runs. Implementation, fixture tests,
mocked subprocess tests, and UI review run locally on the Mac. Hardware
qualification is deferred until the current queue is terminal and the user
authorizes replacing the deployed version.

## Evidence rules

An RTX 3060 has 12 GiB nominal device memory in this study. A comfortable GPU
result requires the whole meeting's output, all enabled inference stages on
CUDA, real time factor at most 1, no recovery retry/fallback or observed external
contention, and at least 15 percent device headroom. Completion with less
headroom remains a fragile success. CPU results use the same time/output rules
and observed host reserve. A two hour deadline is a runtime limit, not an
accuracy measure. A timestamp coverage percentage must distinguish final speech
from the trailing silence in the recording.

Per-run statistics describe the execution that was measured. Reused stages,
missing ownership information, unavailable sensors, legacy runs, other GPU
workloads, and short sampling intervals must remain visible. Summing attempt
peaks is invalid; time averages must be weighted by their measurement intervals.

## Implementation and verification log

- Initial branch: `jaysqvl/run-evidence-and-profile-fixes`, based on main commit
  `06c6be4be904216d830042da2ddc4c80cd2cb21f` (v1.7.6).
- Existing production benchmark: 40 profiles, one recording, fresh checkpoint
  policy, serial execution. Profile disposition and full quality findings are
  awaiting CPU completion.
- Run history uses the submitted profile name and a closed runtime metadata
  projection. A lightweight attempt summary distinguishes retries within a
  stage from intentionally different devices across stages. The resource
  endpoint returns numeric measurements and allowed attempt fields without
  reading checkpoint payloads or returning private adapter parameters.
- Whole-run measurements retain separate invocations across resume. Numeric
  measurements can be retained after cancellation or a deadline, while stale
  owners and deletion reservations still fence writes. Output publication and
  checkpoint ownership rules remain unchanged.
- Local verification includes frontend type checks, compiled Node fixtures,
  ESLint, a Vite production build, Python adapter fixtures, targeted Go API and
  repository tests, and existing ownership/cancellation/recovery tests. These
  checks exercise contracts and UI behavior; they do not establish actual
  inference success, speed, or accuracy on every model.
- Final local results: 95 frontend tests passed; type checks, ESLint, and Vite
  build passed. Python fixtures passed (6 Canary, 16 runtime controls, 3 research
  diarization). Targeted Go tests passed for the picker/resource API,
  measurement persistence, process sampling, batching, and existing recovery,
  cancellation, deadline, deletion, and ownership behavior. `go vet` passed for
  the affected API, repository, transcription, and adapter packages. The server
  binary compiled locally with the freshly embedded frontend assets.
- Generated API documentation includes the authenticated resource endpoint.
  Draft branch changes do not deploy; merge, release, and NAS replacement are
  deferred.

## Failure disposition during preparation

| Observed failure | Prepared response | Remaining gate |
| --- | --- | --- |
| Canary alignment checkpoint rejects a blank aligned word | Normalize blank separator rows; retain strict validation of nonblank rows | Actual recognition, alignment, and diarization on the target hardware |
| DiariZen CUDA OOM is wrapped in `MemoryError`; default pipeline batch differs from the saved attempt | Follow exception causes/contexts for typed memory failures; forward the saved batch to segmentation and embedding | Actual CUDA/CPU completion, recovery behavior, throughput, and peak memory |
| MOSS GPU native output is incomplete; CPU recognition reaches the job deadline | Evidence retained; no output clipping, windowing, or token-budget change guessed | Verify native long-audio/output and speaker stitching contracts |
| Granite base CPU run reaches the deadline during diarization | Evidence retained; interrupted numeric measurements will survive deadlines in the prepared version | Separate recognition cost, diarization cost, and host contention using recorded evidence |
| Granite PLUS CPU output has timestamps outside its audio window | Strict bounds retained; the offending numeric bounds were not recorded in the current failure evidence | Reproduce the bounds without exposing meeting text, then target the adapter defect |
| Transient SQLite queue insertion lock | Tracked separately; no queue changes made during the batch | Reproduce transaction contention and verify an appropriate insertion policy |

The table records defects observed so far. It is not an all-model clearance or a
profile deletion list. Full output quality assessment awaits batch completion.

## Measurement limits and interpretation

- Linux workers are sampled about every 500 milliseconds. Peak values can miss
  short bursts and processes that start and exit between samples. RSS sums can
  count shared pages more than once; these are sampled process RSS statistics.
- CPU percent uses 100 percent for one logical CPU. Capacity comes from CPU
  affinity and cgroup quota. Worker descendants are measured; shared application
  work and short-lived processes outside observed samples are not included.
- Averages are weighted by observed time, and missing observations break an
  interval. Valid zero measurements remain zero; absent data remains unknown.
  Full-run invocation averages include adapter setup and gaps between stages.
- Whole-device VRAM includes unrelated GPU work. Owned VRAM requires matching
  process identity across Docker's PID namespace. If ownership is unavailable,
  the UI leaves owned VRAM unknown and keeps the device measurement separate.
- Existing runs cannot acquire missing historical averages retroactively.
  Reused checkpoints report reuse; they do not fabricate inference cost for the
  current invocation. Peaks across attempts are maxima, never sums.
- Available RAM and GPU headroom are observed reserve, not a demonstrated safe
  batch-size increase. Full host CPU contention is not measured by this change.
  Existing GPU ownership and external workload evidence remains visible.
- Whole-run and stage collectors currently sample independently. The extra GPU
  probes and `/proc` reads require an overhead check on the target hardware
  after the benchmark; no claim of zero overhead is made.
