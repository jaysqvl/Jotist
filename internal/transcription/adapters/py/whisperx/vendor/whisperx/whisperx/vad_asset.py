"""Fetch the original WhisperX VAD asset from a fixed upstream revision.

The source port does not embed this 17 MB checkpoint in every Jotist binary.
Never accept a downloaded or cached checkpoint until its complete hash matches.
"""
import hashlib
import os
from pathlib import Path
import tempfile
from urllib.request import urlopen


VAD_SHA256 = "0b5b3216d60a2d32fc086b47ea8c67589aaeb26b7e07fcbe620d6d0b83e209ea"
VAD_SIZE = 17_719_103
VAD_URL = (
    "https://raw.githubusercontent.com/m-bain/whisperx/"
    "2cfd7b7c5c7bba144954364db747319b50e8232b/whisperx/assets/pytorch_model.bin"
)


def _valid_checkpoint(path):
    if path.is_symlink() or not path.is_file() or path.stat().st_size != VAD_SIZE:
        return False
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest() == VAD_SHA256


def cached_vad_checkpoint(cache_root):
    directory = Path(cache_root) / "jotist-whisperx-vad"
    if directory.is_symlink():
        raise RuntimeError("WhisperX VAD cache must not be a symbolic link")
    directory.mkdir(parents=True, exist_ok=True)
    target = directory / (VAD_SHA256 + ".bin")
    if target.is_symlink():
        raise RuntimeError("WhisperX VAD checkpoint must not be a symbolic link")
    if target.exists():
        if not _valid_checkpoint(target):
            raise RuntimeError("WhisperX VAD cache failed integrity verification; remove the invalid checkpoint and retry")
        return str(target)

    if os.environ.get("HF_HUB_OFFLINE", "").upper() in {"1", "ON", "YES", "TRUE"}:
        raise RuntimeError("WhisperX VAD checkpoint is not cached and offline mode forbids downloading it")

    descriptor, temporary_name = tempfile.mkstemp(prefix=".download-", dir=directory)
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "wb") as destination, urlopen(VAD_URL, timeout=60) as source:
            total = 0
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                total += len(chunk)
                if total > VAD_SIZE:
                    raise RuntimeError("WhisperX VAD download exceeds its expected size")
                destination.write(chunk)
        if not _valid_checkpoint(temporary):
            raise RuntimeError("WhisperX VAD download failed integrity verification")
        # Each concurrent download has its own temporary file and is verified
        # before replacement, so consumers never observe a partial checkpoint.
        temporary.replace(target)
        return str(target)
    finally:
        temporary.unlink(missing_ok=True)
