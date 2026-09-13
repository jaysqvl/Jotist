# DiariZen runtime source

These two MIT packages are vendored together from BUTSpeechFIT/DiariZen commit
`844f5555b0a98acd0931511fc641a8c5b8ba92c7`. `UPSTREAM.json` in each package records
original source hashes, and original copyright and license files are retained.
Model checkpoints are downloaded separately and keep their original license.

DiariZen requires this custom `pyannote.audio` implementation; stock pyannote 4
cannot replace it. Jotist packages the code locally so its reviewed compatibility
changes ship with the executable and do not depend on a moving Git branch.

Compatibility changes use current namespace/package metadata, Hugging Face token
arguments, SoundFile audio metadata, and restricted checkpoint deserialization.
Model layers, state-dict layouts, segmentation settings and clustering algorithms
remain the upstream implementation. The surrounding pyannote core/database/
metrics/pipeline libraries are updated as a coordinated set.
