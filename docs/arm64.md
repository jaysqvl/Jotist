# ARM64 CPU support

The CPU container supports Linux `amd64` and `arm64`. Docker selects the host's
architecture from the CPU image tag. CUDA and Blackwell images remain `amd64`;
this does not add Jetson, ARM CUDA, Apple Metal, or 32-bit ARM support.

The native Linux ARM64 server archive and the Linux ARM64 CLI are separate
artifacts. The server also serves the ARM64 CLI through its normal installer.
The CPU image selects checksum-pinned ARM64 builds of uv and Deno rather than
running x86 executables through emulation.

On Linux ARM64 the default PyTorch backend is CPU. Explicit operator backend
settings are preserved. Existing saved profiles, device locks, queue requests,
context, vocabulary, credentials, and recovery preferences are not migrated or
rewritten. A profile explicitly requiring CUDA still requires a suitable GPU
deployment; choosing ARM does not silently turn that request into a CPU run.

New profile and run drafts use CPU defaults on a Linux ARM64 server. Quick Add
offers Whisper small with CPU DiariZen speakers, or Whisper small without speaker
labels, followed by other CPU starting points. These are editable templates;
they do not replace saved profiles. The server supplies its defaults, so opening
an ARM server from an x86 browser (or the reverse) does not change the target.
Existing AMD64 installations keep their GPU-first templates and defaults.

## Qualification

Native `ubuntu-24.04-arm` CI runners qualify the image, CLI, Python environments
and audio processing. A cross-compile or emulated image boot is not a substitute
for native runtime qualification.

- Container checks cover release identity, health/readiness, both application
  UID/GID configurations, native C++ and Python extension compilation, upload and
  audio persistence, authentication across restart, and the actual ARM CLI.
- All eight Python runtime groups resolve, import and undergo their installed
  dependency audit on ARM64. Inference groups retain native audio decoding.
- Small public speech checks exercise Whisper tiny recognition/word timing and
  standalone DiariZen speaker processing on CPU. Download checks cover manual
  prefetch and offline cache lookup. Numeric inference evidence records the
  architecture, stages, elapsed time and output counts.
- Every image architecture passes its own vulnerability and package-payload
  checks before signing. ARM development headers have a separate exact review;
  AMD64 package evidence cannot qualify ARM64 contents.
- The publisher requires both CPU architectures before creating and signing
  their combined index. It verifies all requested variants and their source
  attestations before moving any version or stable alias.

Runtime installation/import success does not establish every checkpoint's
memory requirements or performance. The inference fixtures are integration
checks, not WER/DER measurements or long-recording benchmarks. Large models may
need more RAM than a small ARM board or standard CI runner provides. Native
BitNet compilation and individual larger checkpoint combinations need their own
hardware qualification before being described as tested.

## Moving an installation

Audio, the database, keys, saved profiles and compatible model weights can be
retained when moving to a new host. Python environments and compiled executables
are architecture-specific: use a separate runtime directory on the new
architecture, rather than sharing an installed x86 `.venv` or native model
executable. Hugging Face weight files can use a retained cache; Jotist downloads
missing model files normally. Keep downloaded and retention settings continue
to protect the selected model sources.

Use the normal [deployment procedure](../deploy/README.md) to back up and verify
persistent data before replacing an existing installation.
