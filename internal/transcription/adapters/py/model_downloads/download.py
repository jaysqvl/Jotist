"""Fetch server-selected weights. Never import or instantiate a model runtime.

The Go caller supplies a curated recipe through stdin, keeps destination leases
until this child exits, and discards stderr. Only bounded, numeric progress and
fixed error classes leave this process. Hugging Face handles cached-file locks,
content-addressed blobs, resumable downloads and revision resolution.
"""
from __future__ import annotations

import errno
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.request import urlopen

from huggingface_hub import hf_hub_download, snapshot_download
from huggingface_hub.constants import HF_HUB_CACHE
from huggingface_hub.errors import (
    GatedRepoError, HfHubHTTPError, RepositoryNotFoundError,
    RevisionNotFoundError, EntryNotFoundError,
)
from tqdm.auto import tqdm

RECORDING_SPACE_RESERVE = 2 * 1024**3


class InsufficientSpaceError(Exception):
    pass


def ensure_space(directory, required):
    if required <= 0:
        return
    directory = Path(directory)
    while not directory.exists():
        directory = directory.parent
    if shutil.disk_usage(directory).free < required + RECORDING_SPACE_RESERVE:
        raise InsufficientSpaceError()


def emit(**fields):
    print(json.dumps(fields, separators=(",", ":")), flush=True)


class Progress:
    def __init__(self):
        self.total = self.done = self.files = self.completed = 0
        self.last = 0.0

    def report(self, phase="downloading", current=0, force=False):
        now = time.monotonic()
        if not force and now - self.last < 0.25:
            return
        self.last = now
        emit(type="progress", phase=phase,
             downloaded_bytes=min(self.total, self.done + max(0, int(current))),
             total_bytes=self.total, completed_files=self.completed,
             total_files=self.files)

    def bar(self):
        owner = self

        class DownloadProgress(tqdm):
            def __init__(self, *args, **kwargs):
                kwargs["disable"] = True
                super().__init__(*args, **kwargs)

            def update(self, n=1):
                # tqdm's disabled mode otherwise skips its own counter.
                self.n += n
                owner.report(current=self.n)

        return DownloadProgress


def regular(path: Path):
    if path.is_symlink():
        raise ValueError("destination must not be a symbolic link")
    if path.exists() and not path.is_file():
        raise ValueError("destination must be a file")


def valid_file(path: Path, artifact):
    regular(path)
    if not path.exists() or path.stat().st_size <= artifact.get("minimum_size", 0):
        return False
    expected = artifact.get("sha256")
    if not expected:
        return True
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    if digest.hexdigest() != expected:
        raise ValueError("existing destination failed integrity verification")
    return True


def destination_parent(path: Path, root: Path):
    # Resolve the operator-configured root (including macOS /var and deliberate
    # cache-root aliases), while rejecting redirection below that trusted root.
    relative = path.relative_to(root)
    if not relative.parts or ".." in relative.parts:
        raise ValueError("destination must stay within its configured root")
    root = root.resolve()
    current = root
    for part in relative.parts[:-1]:
        current = current / part
        if current.is_symlink():
            raise ValueError("destination directory must not be a symbolic link")
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    regular(path)
    return path


def link_weight(source: Path, destination: Path):
    destination.parent.mkdir(parents=True, exist_ok=True)
    regular(destination)
    descriptor, temporary_name = tempfile.mkstemp(prefix=".model-link-", dir=destination.parent)
    os.close(descriptor)
    temporary = Path(temporary_name)
    temporary.unlink()
    try:
        # The staging Hub cache lives under the SAME environment mount. Hard
        # links avoid retaining a second multi-GB copy and are counted once by
        # the storage inventory. Never silently copy across a different volume.
        os.link(source.resolve(strict=True), temporary)
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)


def direct_http(artifact, progress):
    target = destination_parent(Path(artifact["destination"]), Path(artifact["root"]))
    if valid_file(target, artifact):
        size = target.stat().st_size
        progress.total += size
        progress.done += size
        progress.files += 1
        progress.completed += 1
        progress.report(force=True)
        return
    temporary = Path(str(target) + ".tmp")
    regular(temporary)
    # Only fixed public PyTorch/WhisperX asset URLs from the embedded recipe
    # reach this branch. HF credentials are never sent to these other hosts.
    with urlopen(artifact["url"], timeout=60) as response:
        expected_size = int(response.headers.get("Content-Length", "0"))
        if expected_size <= 0 or expected_size > 20 * 1024**3:
            raise ValueError("asset has an invalid size")
        ensure_space(target.parent, expected_size)
        progress.total += expected_size
        progress.files += 1
        digest = hashlib.sha256()
        size = 0
        with temporary.open("wb") as output:
            for block in iter(lambda: response.read(1024 * 1024), b""):
                size += len(block)
                if size > expected_size:
                    raise ValueError("asset exceeds expected size")
                output.write(block)
                digest.update(block)
                progress.report(current=size)
        if size != expected_size or size <= artifact.get("minimum_size", 0):
            raise ValueError("asset download is incomplete")
        if artifact.get("sha256") and digest.hexdigest() != artifact["sha256"]:
            raise ValueError("asset failed integrity verification")
        temporary.replace(target)
        progress.done += size
        progress.completed += 1
        progress.report(force=True)


def hub_artifact(artifact, token, progress):
    target = destination_parent(Path(artifact["destination"]), Path(artifact["root"])) if artifact.get("destination") else None
    kwargs = dict(repo_id=artifact["source"], revision=artifact.get("revision") or "main", token=token)
    if artifact.get("cache_dir"):
        kwargs["cache_dir"] = artifact["cache_dir"]
    if artifact.get("local_dir"):
        kwargs["local_dir"] = artifact["local_dir"]
    files = artifact.get("files")
    # Ignore non-runtime training/export formats. Required .bin/.safetensors,
    # tokenizer/custom-code files and nested pipeline weights remain included.
    ignore = ["*.h5", "*.msgpack", "*.onnx", "*.tflite", "*.ot", "*.gguf", "*.nemo", "*.wav", "*.mp3", "*.flac", "*.mp4", "optimizer*", "scheduler*", "training_args*", "trainer_state*", "runs/*", "logs/*", "checkpoint-*/*"] if not files else None
    planned = snapshot_download(**kwargs, allow_patterns=files, ignore_patterns=ignore, dry_run=True)
    if not planned:
        raise ValueError("model repository has no downloadable runtime files")
    if any(not isinstance(info.file_size, int) or info.file_size < 0 for info in planned):
        raise ValueError("model file metadata is incomplete")
    if artifact["source"] == "mistralai/Voxtral-Mini-3B-2507":
        # The Transformers adapter uses the HF shards. The consolidated export
        # is for a different loader and would duplicate the entire checkpoint.
        planned = [info for info in planned if info.filename != "consolidated.safetensors"]
    # Transformers prefers safetensors when both checkpoint formats exist.
    # Avoid storing the duplicate PyTorch copy for the same model, but retain
    # pyannote/DiariZen's specifically named pytorch_model.bin checkpoints.
    if not files and artifact["source"].split("/", 1)[0] not in {"pyannote", "BUT-FIT", "rewayai", "speechbrain"}:
        if any(info.filename.endswith(".safetensors") for info in planned):
            planned = [info for info in planned if not info.filename.endswith((".bin", ".bin.index.json"))]
    progress.total += sum(info.file_size for info in planned)
    progress.files += len(planned)
    progress.report("checking", force=True)
    if target:
        if len(planned) != 1:
            raise ValueError("a direct weight requires exactly one file")
        # Automatic setup historically checked only a minimum size. A manual
        # check must also match publisher metadata before reusing that file.
        if valid_file(target, artifact) and target.stat().st_size == planned[0].file_size:
            progress.done += planned[0].file_size
            progress.completed += 1
            progress.report(force=True)
            return
    ensure_space(kwargs.get("local_dir") or kwargs.get("cache_dir") or HF_HUB_CACHE,
                 sum(info.file_size for info in planned if getattr(info, "will_download", True)))
    for info in planned:
        file_kwargs = dict(kwargs, revision=info.commit_hash)
        path = Path(hf_hub_download(**file_kwargs, filename=info.filename, tqdm_class=progress.bar()))
        if path.stat().st_size != info.file_size:
            raise ValueError("cached file size does not match model metadata")
        if target:
            if len(planned) != 1 or path.stat().st_size <= artifact.get("minimum_size", 0):
                raise ValueError("direct model file is incomplete")
            link_weight(path, target)
        progress.done += info.file_size
        progress.completed += 1
        progress.report(force=True)
    # Let the official client publish the requested branch/tag reference too.
    # Downloading each file by immutable commit alone would leave no refs/main
    # for a later offline run. An empty include list updates metadata without
    # fetching a second copy of any weight.
    snapshot = Path(snapshot_download(**kwargs, allow_patterns=[], max_workers=1))
    if not artifact.get("local_dir") and snapshot.name != planned[0].commit_hash:
        raise ValueError("model revision changed during download; retry")


def classify(error):
    if isinstance(error, InsufficientSpaceError):
        return "storage_low"
    if isinstance(error, GatedRepoError):
        return "access_denied"
    if isinstance(error, (HTTPError, HfHubHTTPError)):
        status = getattr(error, "code", None) or getattr(getattr(error, "response", None), "status_code", None)
        if status in (401, 403):
            return "access_denied"
    if isinstance(error, (RepositoryNotFoundError, RevisionNotFoundError, EntryNotFoundError)):
        return "not_found"
    if isinstance(error, OSError) and error.errno in (errno.ENOSPC, errno.EDQUOT):
        return "disk_full"
    if isinstance(error, (URLError, TimeoutError, ConnectionError)):
        return "network"
    # httpx is an explicit dependency of huggingface_hub; its exceptions never
    # need to be serialized to identify a network transport failure.
    import httpx
    if isinstance(error, httpx.TransportError):
        return "network"
    return "download_failed"


def main():
    request = json.load(sys.stdin)
    progress = Progress()
    progress.report("checking", force=True)
    token = request.get("token") or None  # normal HF environment/login fallback
    for artifact in request["artifacts"]:
        if artifact.get("url"):
            direct_http(artifact, progress)
        else:
            hub_artifact(artifact, token, progress)
    progress.report("verifying", force=True)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        emit(type="error", code=classify(error))
        raise SystemExit(1) from None
