# Python model runtime maintenance

Jotist uses a shared PyTorch implementation across its model adapters. Separate
Python environments isolate conflicting package requirements; they are not
security sandboxes. Several models already share each environment.

The published RC3 preview corrects an existing-install upgrade problem found
while staging RC2: retained lockfiles kept older dependencies, and readiness
left obsolete packages installed. Its preparation code passed migration of
retained environments as well as fresh-install checks. Deployment acceptance
still requires qualification of the actual published image and installed state.

## Runtime groups

| Environment | Models/integration | Runtime baseline |
| --- | --- | --- |
| NVIDIA ASR | Canary, Parakeet, Sortformer | NeMo 3.0, Transformers 5.17 |
| Canary-Qwen | NeMo HF-SALM | NeMo 3.0, Transformers 5.17, PEFT 0.20 |
| Local ASR | Qwen3, Granite, Voxtral, Cohere, ARK, MOSS | Transformers 5.17, Accelerate 1.15 |
| WhisperX | Whisper recognition, alignment and diarization | Jotist compatibility port of WhisperX 3.8.7rc1 |
| Pyannote | Standard speaker diarization | pyannote.audio 4.0.7 |
| SUPlime | Optional research diarization | SUPlime 0.2, pyannote.audio 4.0.7 |
| DiariZen | Optional research diarization | Bundled DiariZen and its custom pyannote.audio |

All seven environments pin PyTorch 2.14.0, TorchAudio 2.11.0, TorchCodec 0.16.0
and Hugging Face Hub 1.31.0. TorchAudio 2.11 uses PyTorch's stable ABI and supports
Torch 2.11 or later ([official compatibility table](https://docs.pytorch.org/audio/stable/installation.html)); their version numbers no longer need to match. Native
companion wheels must still come from the same CPU/CUDA index. WhisperX also
pins TorchVision 0.29.0.

Every environment that uses Lightning directly pins both `lightning` and
`pytorch-lightning` to 2.6.6. WhisperX and Canary-Qwen also directly pin NLTK
3.10.3. Successful resolution must select these reviewed versions; the verifier
rejects recipes that leave them to transitive resolution. The NLTK pin does not
fix its remaining advisory: the caller-specific audit conditions below still
apply.

Python 3.12 supports all groups. DiariZen requires it. The other groups retain
Python 3.11 support, and the Pyannote recipe also permits 3.10. Modern PyTorch
wheels are unavailable for Intel macOS; run those workers on a supported Linux
host. CPU, CUDA 12.6 (`cu126`), and CUDA 13 (`cu130`) are the supported backends.
Torch 2.14 has no CUDA 12.8 wheel, so that configuration fails with migration
guidance. The Blackwell image uses CUDA 13.0 and requires a compatible NVIDIA
driver. Package resolution alone does not establish that a GPU architecture was
tested.

## Coordinated compatibility changes

WhisperX and DiariZen contain small, explicitly documented compatibility ports.
Their immutable upstream revisions, original licenses, and changes are recorded
in their `vendor` directories. The custom DiariZen pyannote implementation is
required by its model architecture; replacing it with stock pyannote 4 would
change the implementation. Its surrounding runtime is upgraded, and checkpoint
loading uses restricted deserialization with scoped, known local types.

NeMo 3.0 requests Lightning <=2.4 and Hydra <=1.3.2, which retain published
vulnerabilities. The NVIDIA recipes explicitly override only these dependency
bounds, pinning Lightning and PyTorch Lightning 2.6.6 and Hydra 1.3.6. This is a
Jotist-maintained compatibility choice, not a claim that the stock NeMo metadata
supports these versions. Native imports and actual inference must be repeated
when these bounds change. The two-file OneLogger Lightning integration is
bundled with a matching `save_checkpoint` signature; its implementation and
override validation remain intact. A version-checked bootstrap also supplies a
fail-closed placeholder for the removed Neptune logger that NeMo imports eagerly.
Requesting that retired integration raises an error; inference does not enable
it. Original licenses are included in
`THIRD_PARTY_NOTICES.md`.

## Upgrading an existing environment

A hash marker identifies each managed runtime's embedded project definition and
reconciled Python pin. On first adoption, or when either changes, preparation
removes the derived `uv.lock` so uv resolves the updated recipe. The marker is
written atomically. uv synchronizes the existing environment and may rebuild it
when its Python interpreter changes. Downloaded models and application data are
preserved; ordinary preparation with unchanged inputs retains the resolved lock
rather than refreshing all dependencies on every restart.

Readiness synchronizes the installed environment exactly. Where readiness uses
`uv run`, it passes `--exact`, removing packages that are no longer part of the
resolved environment instead of leaving old orphan distributions importable.
This applies even when an earlier attempt already rewrote the project file:
the adoption/hash marker still requires the lock migration. A failed preparation
does not establish readiness and must not be accepted for deployment.

## Checks required for a runtime update

1. Update the full runtime group, including companion wheel indexes and local
   compatibility packages. Do not merge isolated Torch or Transformers bumps
   while their dependent packages still require incompatible versions.
2. Run `python scripts/verify-python-adapter-envs.py --mode lock --python 3.12`
   for each explicit `--torch-index cpu`, `cu126`, and `cu130`. The verifier checks
   resolved versions and copies the actual bundled compatibility source. For
   Python 3.11, select the six supported adapters explicitly, excluding DiariZen.
3. On Linux with FFmpeg, run the same command with `--mode import --torch-index
   cpu`. This installs the resolved graph, imports the actual adapter, and
   exercises TorchCodec and TorchAudio on generated PCM audio. An import that
   merely warns about a broken decoder is insufficient.
4. Audit the installed distributions, including transitive packages. Inspect
   advisory conditions and relevant installed code; distinguish a malformed
   advisory version range from an actual vulnerable implementation. Any
   exception must identify the exact package version, advisory and reviewed
   source/callers, and must be revisited after those change. Pass `--audit` to the
   import verifier to run the installed OSV audit. The policy in
   `scripts/python-adapter-audit-policy.json` expires on its recorded review date
   and requires exact reviewed source hashes. Network failure or a new finding
   fails the check; matched exceptions remain visible in the JSON report.
5. Load the actual model checkpoints and run recognition/alignment/diarization
   on a known public fixture. On supported GPU hardware, repeat CUDA paths and
   CPU recovery where relevant. Preserve precision, model revisions, alignment
   and chunking settings. A short smoke test establishes integration, not WER,
   DER, or accuracy on a user's meetings.
6. Test migration using a private copy of existing runtime volumes as well as a
   fresh environment. After exact readiness, compare the complete installed
   package/version graph with the reviewed qualification graph and run the
   installed audit. Review and requalify any difference, including extra
   packages; checking only the directly pinned versions is insufficient.
7. Run the backend tests and vulnerability check, publish from the reviewed
   source commit, and qualify the resulting image before production cutover.
   After cutover, require the full live installed graph to equal the qualified
   staged graph and repeat the installed audit with the actual caller files.

CI resolves all supported backend/Python combinations and automatically runs
Linux CPU imports, native decoding, and installed dependency audits for adapter pull requests. GPU and full
checkpoint qualification require the corresponding hardware and model assets.

NLTK 3.10.3 still has an unpatched model-persistence advisory. Its exceptions
apply only to the reviewed WhisperX and Canary-Qwen paths and require matching
hashes of both installed library code and the actual materialized Jotist caller
scripts. Missing or changed callers invalidate the exceptions. This differs
from the Lightning 2.6.6 exception for a malformed advisory version range.

Direct security-sensitive dependencies are pinned. Other transitive requirements
still resolve through uv at installation; the saved validation graphs describe
what was actually tested, rather than promising every future installation has
an identical full dependency graph.

Dependency updates do not remove the existing authority of model subprocesses.
Separating workers from application credentials/data remains an architectural
hardening task. Model weight licenses and remote model-code review also remain
separate from dependency version checks.
