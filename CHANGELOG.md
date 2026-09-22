# Changelog

## [1.8.0](https://github.com/jaysqvl/Jotist/compare/v1.7.2...v1.8.0) (2026-09-22)


### Features

* preserve local speech models and recoverable execution ([9262941](https://github.com/jaysqvl/Jotist/commit/9262941e74ded0845884954b229ea554da15675e))
* publish a rolling development CUDA image ([d7ad8ee](https://github.com/jaysqvl/Jotist/commit/d7ad8ee177ec8dae9b6b45cb625f80bb6dc040c6))
* rebrand independent Jotist app and release pipeline ([7d09213](https://github.com/jaysqvl/Jotist/commit/7d09213e4dd4a4a1354a8aee40840be05453a20a))


### Bug Fixes

* bind audit exceptions to deployed application callers ([35ae456](https://github.com/jaysqvl/Jotist/commit/35ae45682f295463fa4288b2239c03fcc31b2158))
* calibrate speaker memory guidance from NAS qualification ([b288dd4](https://github.com/jaysqvl/Jotist/commit/b288dd47e03d9cc49b99e021fac5d2f325241666))
* give browser sessions one owner and preserve upload behavior ([3dc4c51](https://github.com/jaysqvl/Jotist/commit/3dc4c510cdf54aeeeaf90c59f22056d80064dfe2))
* make upload completion and recording deletion atomic ([d92f7aa](https://github.com/jaysqvl/Jotist/commit/d92f7aa3c2fd08b6da914a024df57e1fc6edf562))
* migrate existing runtime dependency graphs exactly ([71d31c7](https://github.com/jaysqvl/Jotist/commit/71d31c7c3becc91f217b7d5bca37f3a2db86bdd4))
* organize repository sources and deployment entrypoints ([d3bcafd](https://github.com/jaysqvl/Jotist/commit/d3bcafd703f219de37a760de810015cb250f39b0))
* provide CTranslate2 cuBLAS compatibility in CUDA 13 image ([da7cb34](https://github.com/jaysqvl/Jotist/commit/da7cb34945f0f32586c61a4309b7e2bbd9181288))
* recover Cohere Auto token cutoffs within decoder context ([dd79dc4](https://github.com/jaysqvl/Jotist/commit/dd79dc46bded509b86e686b1d4de581310ea015f))
* refresh retained runtime locks and remove obsolete packages ([0a5b378](https://github.com/jaysqvl/Jotist/commit/0a5b3783cc8e6585d8c96865c2075c3d23cabbeb))
* restore CPU Canary profiles and expose actionable recovery controls ([e9ffe35](https://github.com/jaysqvl/Jotist/commit/e9ffe353d27c399c5c4b51a2101927314bbc942d))
* update model runtimes and enforce dependency security checks ([7d67b9b](https://github.com/jaysqvl/Jotist/commit/7d67b9b3b401c591e3b10b2252b304ded755e41d))
* update model runtimes and enforce dependency security checks ([bca181e](https://github.com/jaysqvl/Jotist/commit/bca181ef944f208182b3190b916e9ee0095bd318))

## [1.7.2](https://github.com/jaysqvl/Jotist/compare/v1.7.1...v1.7.2) (2026-09-22)

- Restore saved CPU Canary profiles and make recovery controls easier to use.
- Show word-aligned transcripts as readable speaker turns, without repeated words, and clarify the models recorded for each run.
- Retry Cohere Auto token cutoffs within the model's decoder limit and report the failed audio window if a cutoff persists.
- Update speaker memory guidance using NAS measurements.

Back up existing data and model environments before upgrading. A window that reaches the model's decoder limit still fails rather than publishing incomplete text; combined recognition stages have no per-window checkpoint. See [recoverable transcription](https://github.com/jaysqvl/Jotist/blob/v1.7.2/docs/recoverable-transcription.md).

## [1.7.1](https://github.com/jaysqvl/Jotist/compare/v1.7.0...v1.7.1) (2026-09-14)

- Move container files to `deploy/` and remove unused assets and editor files.
- Keep web tooling with each app and check Compose configurations in CI.

Alternate Compose commands now need `--project-directory .` from the repository root. Keep the same project name and storage mappings; see the [deployment guide](https://github.com/jaysqvl/Jotist/blob/v1.7.1/deploy/README.md). Use `make build` instead of the root `build.sh`. Application behavior and model runtimes are unchanged.

## [1.7.0](https://github.com/jaysqvl/Jotist/compare/v1.6.1...v1.7.0) (2026-09-14)

- Introduce Jotist's name, indigo styling, and independent releases.
- Add more local models, saved profiles, meeting context, and vocabulary settings.
- Compare multiple transcription runs, queue profiles, and recover supported runs from checkpoints.
- Fix session refresh, upload retries, recording deletion, and audio playback.
- Update model dependencies and improve upgrades of existing model environments.

Back up existing data and model environments before upgrading; the `scriberr` CLI remains compatible. See the [migration guide](https://github.com/jaysqvl/Jotist/blob/v1.7.0/docs/jotist-migration.md).

NLTK's advisory remains unpatched, with narrowly reviewed [dependency exceptions](https://github.com/jaysqvl/Jotist/blob/v1.7.0/scripts/python-adapter-audit-policy.json). Model workers share the app's permissions; Blackwell inference remains unverified. Model weights have [separate license terms](https://github.com/jaysqvl/Jotist/blob/v1.7.0/THIRD_PARTY_NOTICES.md).

## [1.6.1](https://github.com/jaysqvl/Jotist/compare/v1.6.0...v1.6.1) (2026-09-05)


### Bug Fixes

* **audio:** restore streaming and responsive layouts ([912a67e](https://github.com/jaysqvl/Jotist/commit/912a67ed225d489c82c81d09d29719df3d26594c))

## [1.6.0](https://github.com/jaysqvl/Jotist/compare/v1.5.17...v1.6.0) (2026-09-05)


### Features

* **queue:** add sequential transcription runs ([ccc8f46](https://github.com/jaysqvl/Jotist/commit/ccc8f46f68fd994140b883212f789756ef8cd65f))


### Bug Fixes

* **transcription:** harden execution lifecycle ([90df973](https://github.com/jaysqvl/Jotist/commit/90df9735015a0ef1f594ed9401e97baf0729aa31))
* **transcription:** prevent blank TXT and SRT downloads ([39f9ac9](https://github.com/jaysqvl/Jotist/commit/39f9ac99abf993691546be97b1c1d9a07bf3a031))
