# Recoverable transcription: current implementation and operations

Implementation status: 2026-09-12. This guide describes recoverable transcription and Levels 1–4 in the working tree, following the [technical design](design/adaptive-transcription-pipeline.md). Real Canary CPU/GPU qualification is documented separately in [the qualification report](canary-stage-qualification.md). Production deployment remains separate.

## What is recoverable today

The normal database-backed service saves completed adapter results before a downstream stage runs. Checkpoints are private serialized results, separate from downloaded model weights and GPU memory.

| Boundary | Current behavior |
| --- | --- |
| Canary recognition | Independent checkpoint containing the original native hypotheses, cut/sample identities and partial text; the recognizer exits before alignment. |
| Canary CTC alignment | Independent worker restores only the exact existing CTC artifact/tokenizer and consumes the selected recognition checkpoint. Failure preserves recognition. |
| Other recognizers and requested internal alignment | One **combined adapter stage** unless the adapter declares a real split. A checkpoint exists only after that call returns successfully. |
| Native or inline speaker processing | Part of that combined result when the recognizer runs it internally. |
| Standalone speaker diarization | An independent checkpoint after the diarizer returns; a failure here preserves an already completed recognition result. |
| Multiple audio tracks | Separate `track-<hashed track identity>.…` nodes under the exact parent execution. Completed tracks retain their selections when the parent resumes. |
| Preparation and final assembly | Source/prepared audio are hashed; preprocessing can run again. Final speaker assignment/assembly and publication use completed results, without a separately advertised recoverable stage. |

Canary preserves its native recognition chunking, timestamp prompt and numerical policy. Its separate CTC stage starts with the recorded native chunk batch and FP32 precision. Disabling timestamps skips alignment. The recovery view chooses alignment over recognition for a track when both exist, and retains recognition if alignment fails. Distinct audio tracks are never deduplicated merely because their words match.

Executions and stages have distinct identities. An attempt commits only while its exact execution generation remains authorized, uncancelled and within its saved deadline. Cancellation fences stale writers before their processes stop. A generation change alone does not authorize overlapping attempts: the previous attempt must have stopped and been terminalized.

## Modes, scheduling and limits

| `recovery_mode` | Automatic behavior |
| --- | --- |
| `fixed` | Checkpoints, strict reuse and shared GPU admission. No automatic device, precision, batch or window change. |
| `stage_management` | The same behavior plus one eligible cleanup retry following structured CUDA memory/runtime failure, using the same settings. |
| `batch_management` | Level 2 adds up to two adapter-qualified smaller batches within the configured minimum. |
| `cpu_fallback` | Level 3 adds one CPU attempt for each explicitly allowed, unlocked stage, with a selected supported CPU precision. |
| `shorter_windows` | Level 4 adds up to two explicitly selected, qualified windows with validated overlap/stitching, after permitted full-window capacity attempts are exhausted. |
| Empty or omitted | Preserves legacy Auto behavior, including its eligible CUDA-to-CPU FP32 retry. This is not the new fixed policy. |

For explicit plans, Auto resolves against the saved GPU inventory and preserves the requested starting precision. CPU fallback requires Level 3 or 4 plus per-stage permission and explicit precision; a device lock prevents it. Fixed mode executes saved concrete overrides without further adaptation. Unsupported CPU precision is rejected rather than silently converted. Models, language, prompts, vocabulary, decoding, VAD and speaker settings are not adaptive controls. Legacy Auto retains its existing GPU-to-CPU FP32 behavior; a Canary recognition fallback also keeps subsequent legacy alignment on CPU.

The current scheduler admits one Scriberr GPU operation at a time, conservatively across all GPU ordinals. Queue workers, quick processing, track children and runtime preparation use that admission path. External applications remain outside this lock. Before launch, low measured headroom causes a bounded wait: the reserve is at least 1 GiB or 15% of capacity. CPU admission also checks host/cgroup headroom when available. Unavailable readings remain unknown and never create a fit guarantee.

Limits are currently code-defined:

- GPU admission waits at most ten minutes, bounded by the enclosing context.
- The deterministic maximum ladder is initial → cleanup → two smaller batches → CPU → two shorter windows. Unsupported, locked or unpermitted candidates are skipped. The saved budget spans resumes.
- Only structured CUDA OOM/runtime evidence authorizes GPU recovery; batch/window reductions require a capacity failure. CPU window reduction requires structured host allocator OOM. Dependency, model access, input, cancellation and deadline errors do not qualify.
- Confirmed external GPU contention does not justify reducing model context or learning a clean capacity result. Docker/NVML PID ownership uncertainty is recorded separately, permits the eligible recovery ladder, and prevents promotion.
- Each stage has a saved seven-attempt ceiling, including attempts across explicit resumes. A resume does not reset the count. Waiting attempts can consume an attempt number.
- Checkpoint persistence can be retried three times using the same attempt and output, without rerunning inference.
- Execution deadlines are inherited from the processing context, or default to 24 hours when none exists. Queue/quick processing can impose the configured media timeout. Resume preserves the original deadline.

Cancelled executions cannot resume. An expired deadline or exhausted attempt budget requires a new run; a new run can reuse only qualified compatible artifacts.

## Exact reuse and fresh runs

Reuse is restricted to the same recording. Compatibility includes original/prepared audio hashes, preprocessing, relevant settings, adapter/runtime identity and numerical/device policy. Prompts and vocabulary affect the settings hash; credentials and callback destinations do not. Manifests contain hashes and controlled provenance, not credentials or raw prompts. Result files contain private transcript content and follow the recording's access boundary.

Cross-execution reuse currently requires a declared immutable model revision and a qualified runtime fingerprint, including the server binary and available lock/runtime files. An unresolved model revision is scoped to its original execution. Unqualified auxiliary alignment/diarization paths and hosted recognition do not receive cross-execution reuse. These conservative restrictions mean a valid earlier result may still require recomputation in a new run.

Same-execution resume uses its already selected immutable checkpoint after input/settings/runtime and integrity validation. A changed multi-track layout (track identities, names or offsets) requires a new execution. It does not silently switch to the newest result. A fresh run gets a distinct artifact ID even when its compatibility key matches an earlier run. Corrupt/missing selected data blocks that resume and requires recomputation through a new run.

`reuse_checkpoints` is an optional boolean. Omitted/`true` permits qualified reuse; `false` forces fresh artifacts without disabling persistence. Queue and immediate start/rerun requests accept this override when using a saved profile, without rewriting that profile. For example, the existing authenticated queue endpoint accepts:

```http
POST /api/v1/transcription/<recording-id>/queue
Content-Type: application/json

{"profile_id":"<saved-profile-id>","reuse_checkpoints":false}
```

## Storage configuration and cleanup

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `CHECKPOINT_DIR` | `checkpoints` beside the configured transcript/output directory | Persistent checkpoint root. |
| `CHECKPOINT_MAX_BYTES` | `5368709120` (5 GiB) | Total checkpoint-file quota. Explicit `0` means unbounded; negative or invalid values are rejected. |

Place the root on persistent storage and retain it with the database. Moving only the database leaves missing checkpoint references. The directory layout uses a hash of the recording identity rather than a source filename:

```text
<root>/<recording-sha256>/<compatibility-sha256>/<checkpoint-uuid>/
    manifest.json
    result.json
    auxiliary-artifacts/  # when a caller supplies them
```

Files are written in a private temporary directory on the same filesystem, validated and flushed, then atomically renamed. The database advertises the checkpoint only after a fenced transaction succeeds. Checksums cover the manifest, result and every auxiliary file. Timestamp validation rejects missing/null/nonfinite/out-of-range bounds while preserving valid overlapping turns. Selected downstream artifacts also validate their upstream files.

A crash between rename and database commit leaves an unreferenced artifact, never a reusable database hit. Startup cleanup retries pending recording deletions and collects eligible storage. Default ages are 30 days for unreferenced committed artifacts and 24 hours for abandoned temporary/orphan artifacts. These ages are repository policy defaults, not additional environment variables. Quota pressure may collect eligible unreferenced artifacts sooner.

Retained execution selections, their upstream dependency closure and active attempts are protected. If protected data fills the quota, persistence fails clearly; it does not discard recoverable work or continue without its checkpoint. Startup collection can also block inference dispatch while the UI remains available. Increase the quota or delete recordings through the normal application flow, then restart with the corrected configuration. There is no dedicated public checkpoint-GC endpoint or periodic GC scheduler in this rollout.

Recording deletion first writes a durable tombstone to reject late commits, then removes that recording's derived files and references. A filesystem failure leaves retryable cleanup; the normal delete endpoint retains the original recording and asks the operator to retry. Startup retries tombstoned cleanup. Checkpoint GC never removes source audio, published transcripts or model-weight caches as a space remedy. A source-recording deletion remains the application's separate authorized operation.

## Restart, resume and publication

The server holds an exclusive `<database path>.server.lock` lease. This supports one Scriberr coordinator per deployment; multiple independent databases do not share GPU admission. File-lock acquisition alone does not prove old inference workers have exited.

On Linux, a verified clean shutdown or a changed boot/PID-namespace/PID-1 boundary supplies the current restart proof. An unclean restart inside the same process/container boundary can leave old workers unverified. In that case inference pauses while the UI remains accessible for inspection. Restart the container so its prior worker processes are terminated; a host-native deployment may require a host restart to establish a changed boundary. Do not remove the lease file to bypass this check. Broader multi-server coordination and independent orphan-process adoption are not implemented.

The startup barrier runs before ordinary queue recovery and promotion. Interrupted executions remain visible and require explicit same-plan resume. The existing authenticated API exposes:

- `GET /api/v1/transcription/:id/runs/:run_id/recovery`: actual stages/attempts, resume eligibility and reason, and available partial transcript. Older executions have no retroactive checkpoints.
- `POST /api/v1/transcription/:id/runs/:run_id/resume`: resumes the exact saved execution/settings after ownership, deadline, budget and checkpoint checks. It accepts no quality/context overrides. Required credentials are resolved through the existing authorized configuration; changed settings require a new run.

Existing completed/pinned execution results and the last published transcript remain available while a replacement runs or fails. Requested output must exist before the new execution is published complete; successful publication clears any summary associated with the replaced transcript. Partial text can be viewed and downloaded from the selected run’s recovery panel. Stage states describe actual storage boundaries (`pending`, `waiting_for_resource`, `running`, `succeeded`, `retryable`, `blocked`, `failed`, `cancelled`, `interrupted`). Each immutable attempt records the actual device, precision, batch, window, overlap, stitching version, reason and measurements.

Quick transcription uses shared admission and temporary checkpointing during processing. After processing returns, it removes its temporary database/checkpoint state. It does not offer retained-run resume after completion. Its existing in-memory result/file lifetime is six hours from creation, with hourly expired-item cleanup; it is not a durable recording workflow.

Completion callbacks receive one durable dispatch claim per execution. A crash after the claim can lose delivery: this is not a transactional outbox. The existing webhook sender may make up to three HTTP attempts, so receivers should deduplicate by `metadata.execution_id` or the stable `Idempotency-Key: scriberr-execution-<execution-id>` header. Partial results do not trigger completed-result callbacks.

## Learning, measured capacity and fixed profiles

Learning is optional per saved profile. Admission captures the exact profile revision, learning generation and selected immutable plan IDs before work is queued. Explicit parameter requests cannot forge that identity; editing a profile or promoting a later plan never changes an already admitted run. A stage that has begun keeps its immutable initial plan on resume, even if fresh capacity differs.

Observations contain controlled settings, hashes, workload buckets, duration/channels and measurements; they contain no transcript, prompt, token or audio path. Scope includes fixed settings, exact model artifacts/runtime, hardware and memory capacity, stage, source window and workload. A successful adapter result is not a WER measurement. Cache hits, imported/synthetic data, partial work, unknown ownership/measurements, cancellation and external contention do not establish clean learning evidence.

Promotion needs at least three eligible full-stage successes across at least two recordings, measured peak/time and a reserve of at least 1 GiB or 15% of capacity. A starting plan must still fit fresh available memory and current stage permissions. CPU plans use their own host/cgroup capacity domain while retaining the original fixed-settings identity. There is no silent model or precision substitution.

A learned shorter-window start additionally retains failure evidence for **every permitted full-window candidate**, including CPU when allowed. GPU and host proof must match the same fixed/runtime/model/hardware/workload identity and record the appropriate OOM. Selection requires fresh readings for every exhausted memory domain; missing readings, changed capacity, or more available memory than the retained failures return to the full-window plan. A successful shorter window alone cannot establish exhaustion or an accuracy improvement.

The profile's **Learning** view exposes observations, supporting plan IDs, selected plans and saved revisions. **Freeze** materializes the selected plan and inherited transcription context into a new fixed revision. **Reset** advances the learning generation and clears selection without erasing history. **Restore** creates a new revision from stored parameters. Actions use revision/generation compare-and-swap checks. Metadata-only profile edits preserve compatible selections; numerical/context changes invalidate them conservatively. Credentials remain governed by the saved/default token controls.

The authenticated profile endpoints are `GET /api/v1/profiles/:id/adaptive-policy`, `POST .../freeze-adaptive`, `POST .../reset-adaptive`, and `POST .../restore-revision`. Action bodies include positive `expected_revision` and `expected_generation`; Freeze also takes `plan_id`, Restore takes `revision`.

Linux sampling reports host/cgroup RAM, verified process RSS, device-wide GPU usage and verified process VRAM separately. Canary additionally emits process-owned RSS and PyTorch allocated/reserved peaks. Polling can miss brief allocations; PyTorch values exclude non-PyTorch/driver allocations. Container PID namespace uncertainty is shown explicitly and prevents learning promotion rather than being mislabeled external contention. Selector memory estimates remain separate from these measured attempts.

## Adapter qualification and validation

All controller levels are implemented. Controls are gated by concrete adapter declarations, not an assumed equivalence between similarly named batch/window arguments. Canary CTC currently qualifies native batch reduction to 1 and 20/10-second encoder windows with two seconds of context per side, unchanged full-cut Viterbi and exact frame-grid stitching. Other adapters expose their actual combined or diarization boundary and explicit CPU precision support; undeclared batch/window reductions remain unavailable.

Real public-audio Canary tests on the Ryzen 7 5700G/RTX 3060 verified original-versus-split parity, CPU/GPU CTC batch reduction, short inputs, reduced-window stitching and CPU alignment from a saved recognition artifact after an actual constrained GPU OOM. Reduced windows retained all words on those fixtures but shifted some word boundaries by up to 0.24 seconds. These fixtures are not long-meeting WER/DER benchmarks. See [exact results and memory figures](canary-stage-qualification.md).

Local automated coverage includes ownership/cancellation/deadline fences, atomic artifact failures, checksums/upstream integrity, quota/deletion cleanup, strict selected/fresh reuse, the complete seven-attempt ladder, resume after failed alignment/diarization, canonical stage settings, workload-scoped learning, mixed-domain exhaustion proof, immutable admission, freeze/reset/restore and profile revision conflicts. UI tests cover the selectable levels, qualified controls, measured/unknown memory presentation and learning actions. Rendered preview and final suite results are recorded in the task's completion report.

Operational limits: one coordinator per database/deployment, conservative global GPU admission, no independent orphan-worker adoption, and temporary quick-transcription results rather than retained-run resume. Production deployment and long-meeting accuracy/peak-memory evaluation remain separate activities.

### Completed validation, 2026-09-12

- Full Go package/test suite passed: `go test ./api-docs ./cmd/... ./internal/... ./pkg/... ./tests -count=1`.
- `go vet` passed across the same scope. Repository, API and transcription suites passed with `-race`; adapter/registry and GPU ownership classifier race checks also passed.
- Python contract suite passed: 62 tests and 23 subtests. Four existing NVIDIA hardware tests require separately installed local model environments and were not run successfully on the Mac; actual staged Canary CPU/GPU tests ran in the isolated NAS workers documented above.
- Frontend: 72 tests, full lint and production TypeScript/Vite/PWA build passed.
- LAN UI at `192.168.10.8:8193`: created and reopened a saved Level 4 profile, verified CPU permissions/window bounds and invalid-configuration save blocking, confirmed GPU selection updates precision defaults, and used the real Reset endpoint (generation 1 → 2). Desktop and 390×844 mobile learning/profile dialogs were rendered and inspected. Freeze/promotion and restore were verified by API/repository tests; the preview has no fabricated qualified observations.
- The preview uses its own database, no model workers, and explicitly synthetic recovery-demo text. Production data/configuration were not changed.
