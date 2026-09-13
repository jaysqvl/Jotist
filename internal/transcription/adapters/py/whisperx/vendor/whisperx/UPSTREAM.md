# Jotist WhisperX compatibility port

Source: https://github.com/m-bain/whisperx

Immutable revision: `2cfd7b7c5c7bba144954364db747319b50e8232b` (upstream version
`3.8.7rc1`). Jotist identifies this modified package as `3.8.7rc1+jotist.1`.
The original BSD 2-Clause license and upstream README are retained. This is a
Jotist-maintained compatibility port, not an upstream WhisperX release.

Changes from that revision:

- Declare the reviewed modern runtime tuple explicitly: Torch 2.14.0,
  TorchAudio 2.11.0, TorchVision 0.29.0, TorchCodec 0.16.0,
  Transformers 5.17.0, and Hugging Face Hub 1.31.0. The outer Jotist project
  chooses CPU/CUDA wheel sources. No dependency-constraint override is used.
- Fetch the identical upstream VAD checkpoint lazily at its immutable source
  URL instead of including a 17 MB weight file in Jotist's source/binary.
  `vad_asset.py` verifies the exact length and SHA-256 before using downloaded
  or cached bytes, rejects cache symlinks, and publishes downloads atomically.
  A missing checkpoint fails without network access when `HF_HUB_OFFLINE` is set.
- Normalize trailing whitespace in the copied Python sources.

Model weights and model selection are unchanged. Runtime compatibility must
be validated separately from successful dependency resolution; see Jotist's
runtime validation record for the actual checks completed on a release.
