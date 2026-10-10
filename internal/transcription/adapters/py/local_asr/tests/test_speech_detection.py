"""Timeline, routing and failure contracts; real detector validation is separate."""
import hashlib
import importlib
import json
from pathlib import Path
import sys
import types
import weakref

import numpy as np
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import speech_detection as vad
import transcribe
from diagnostics import safe_failure


def test_short_utterances_are_padded_and_merged_without_changing_source():
    contract = vad.default_contract()
    assert contract["min_speech_duration_ms"] == 0
    sr = 16000
    source = np.arange(8 * sr, dtype=np.float32)
    before = hashlib.sha256(source.tobytes()).hexdigest()
    # Brief responses under 250 ms must reach the recognizer, including at EOF.
    ranges = vad.padded_ranges([{"start": sr, "end": sr + 1600},
                               {"start": 2 * sr, "end": 2 * sr + 800},
                               {"start": 8 * sr - 800, "end": 8 * sr}], len(source), sr, contract)
    assert ranges == [(8000, 40800), (119200, 128000)]
    assert all(any(start <= item["start"] < item["end"] <= end for start, end in ranges)
               for item in [{"start": sr, "end": sr + 1600}, {"start": 8 * sr - 800, "end": 8 * sr}])
    assert before == hashlib.sha256(source.tobytes()).hexdigest()


@pytest.mark.parametrize("intervals", [[{"start": True, "end": 4}], [{"start": 2, "end": 1001}],
                                      [{"start": 2, "end": 7}, {"start": 6, "end": 9}]])
def test_invalid_detection_does_not_become_a_partial_transcript(intervals):
    with pytest.raises(vad.AudioPreparationError):
        vad.padded_ranges(intervals, 1000, 16000, vad.default_contract())


def test_cohere_preparation_cannot_be_disabled_by_request(tmp_path):
    path = tmp_path / "request.json"
    path.write_text(json.dumps({"model_id": "CohereLabs/cohere-transcribe-03-2026", "align_words": False}))
    config = transcribe.load_config(path)
    assert vad.required_contract(config) == vad.default_contract()
    for invalid in (None, {}, {**vad.default_contract(), "threshold": 0.99}):
        config["speech_detection"] = invalid
        path.write_text(json.dumps(config))
        with pytest.raises(vad.AudioPreparationError):
            transcribe.load_config(path)
    assert vad.required_contract({"engine": "qwen"}) is None


def pipeline(monkeypatch, tmp_path, ranges, *, align=False):
    sr = 16000
    source = np.arange(12 * sr, dtype=np.float32) / (12 * sr)
    monkeypatch.setitem(sys.modules, "soundfile", types.SimpleNamespace(read=lambda *a, **k: (source, sr)))
    monkeypatch.setitem(sys.modules, "torch", types.SimpleNamespace(
        cuda=types.SimpleNamespace(is_available=lambda: False), float32="torch.float32"))
    monkeypatch.setattr(transcribe.subprocess, "run", lambda *a, **k: types.SimpleNamespace(returncode=0))
    audio_path = tmp_path / "source.wav"
    audio_path.write_bytes(b"fixture")
    calls, refs = [], []
    def detect(audio, rate, device, contract, **kwargs):
        assert audio is source and rate == sr and device == "cpu"
        if isinstance(ranges, Exception):
            raise ranges
        return ranges, {"speech_detection_device": device, "speech_detection_range_count": str(len(ranges))}
    monkeypatch.setattr(transcribe, "detect_speech", detect)
    class Backend:
        def transcribe(self, sample, rate):
            calls.append(sample.copy())
            return {"text": "hello", "language": "en"}
    def factory(*a):
        backend = Backend()
        refs.append(weakref.ref(backend))
        return backend
    monkeypatch.setattr(transcribe, "create_backend", factory)
    class Aligner:
        def align(self, sample, text, language, rate):
            assert refs[0]() is None
            return [{"word": "hello", "start": 0.25, "end": 0.5}]
    monkeypatch.setattr(importlib.import_module("qwen_backend"), "create_aligner", lambda *a: Aligner())
    config = {"model_id": "cohere-fixture", "engine": "cohere", "audio_file": str(audio_path),
              "device": "cpu", "precision": "float32", "context_mode": "none", "language": "en",
              "default_chunk_seconds": 30, "max_chunk_seconds": 30, "align_words": align}
    return source, calls, refs, config


def test_preparation_preserves_offsets_and_does_not_concatenate_speech(monkeypatch, tmp_path):
    source, calls, refs, config = pipeline(monkeypatch, tmp_path, [(16000, 48000), (128000, 176000)], align=True)
    before = source.tobytes()
    result = transcribe.execute(config)
    assert [(w["start"], w["end"]) for w in result["word_segments"]] == [(1.25, 1.5), (8.25, 8.5)]
    np.testing.assert_array_equal(calls[0], source[16000:48000])
    np.testing.assert_array_equal(calls[1], source[128000:176000])
    assert source.tobytes() == before
    assert result["metadata"]["duration_seconds"] == "12.0"
    assert refs[0]() is None
    assert "auto_token_split_windows" not in result["metadata"]


def test_no_detected_speech_returns_empty_success_without_loading_asr(monkeypatch, tmp_path):
    _, calls, refs, config = pipeline(monkeypatch, tmp_path, [])
    result = transcribe.execute(config)
    assert not calls and not refs
    assert result["text"] == "" and result["segments"] == []
    assert result["metadata"]["chunk_count"] == "0"
    assert result["metadata"]["duration_seconds"] == "12.0"


def test_required_preparation_failure_is_private_and_stops_before_asr(monkeypatch, tmp_path):
    _, calls, refs, config = pipeline(monkeypatch, tmp_path, vad.AudioPreparationError("private transcript and token"))
    with pytest.raises(vad.AudioPreparationError) as failure:
        transcribe.execute(config)
    assert not calls and not refs
    diagnostic = safe_failure(failure.value, config["_diagnostic_phase"])
    assert diagnostic["phase"] == "audio_preparation"
    assert diagnostic["diagnostic_code"] == "runtime_audio_preparation"
    assert "private" not in json.dumps(diagnostic)


def test_wrong_runtime_version_fails_before_model_load(monkeypatch):
    monkeypatch.setattr(vad.metadata, "version", lambda _: "old-version")
    with pytest.raises(vad.AudioPreparationError, match="pinned version"):
        vad.detect_speech(np.ones(16000, dtype=np.float32), 16000, "cuda", vad.default_contract())


def test_corrupt_detector_asset_fails_before_deserialization(monkeypatch):
    monkeypatch.setattr(vad.metadata, "version", lambda _: vad.default_contract()["package_version"])
    package = types.ModuleType("silero_vad")
    package.__path__ = []
    monkeypatch.setitem(sys.modules, "silero_vad", package)
    monkeypatch.setitem(sys.modules, "silero_vad.utils_vad", types.SimpleNamespace(get_speech_timestamps=None))
    monkeypatch.setitem(sys.modules, "torch", types.SimpleNamespace())
    class CorruptAsset:
        def joinpath(self, name): return self
        def read_bytes(self): return b"wrong model bytes"
    monkeypatch.setattr(vad.resources, "files", lambda _: CorruptAsset())
    with pytest.raises(vad.AudioPreparationError, match="integrity check"):
        vad.detect_speech(np.ones(16000, dtype=np.float32), 16000, "cpu", vad.default_contract())


def test_preparation_diagnostics_cannot_be_mislabeled_as_configuration():
    result = safe_failure(vad.AudioPreparationError("private recording"), "configuration")
    assert result["phase"] == "audio_preparation"
    assert result["exception_class"] == "AudioPreparationError"
    assert "private" not in json.dumps(result)
