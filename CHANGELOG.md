# Changelog

## [1.7.2](https://github.com/jaysqvl/Jotist/compare/v1.7.1...v1.7.2) (2026-09-22)


### Bug Fixes

* calibrate speaker memory guidance from NAS qualification ([b288dd4](https://github.com/jaysqvl/Jotist/commit/b288dd47e03d9cc49b99e021fac5d2f325241666))
* recover Cohere Auto token cutoffs within decoder context ([dd79dc4](https://github.com/jaysqvl/Jotist/commit/dd79dc46bded509b86e686b1d4de581310ea015f))
* restore CPU Canary profiles and expose actionable recovery controls ([e9ffe35](https://github.com/jaysqvl/Jotist/commit/e9ffe353d27c399c5c4b51a2101927314bbc942d))

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
