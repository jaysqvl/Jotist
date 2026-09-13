"""Alignment-only orchestration tests; no checkpoint or runtime downloads."""
import sys
import types
import json
from pathlib import Path

import numpy as np
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import align_transcript
import qwen_backend
from backends import RecognitionError


def alignment_fixture(monkeypatch, tmp_path):
    audio = np.ones(16000 * 5, dtype=np.float32)
    monkeypatch.setitem(sys.modules, "soundfile", types.SimpleNamespace(read=lambda *args, **kwargs: (audio, 16000)))
    monkeypatch.setitem(sys.modules, "torch", types.SimpleNamespace(cuda=types.SimpleNamespace(is_available=lambda: False), float32="torch.float32"))
    monkeypatch.setattr(align_transcript.subprocess, "run", lambda *args, **kwargs: types.SimpleNamespace(returncode=0))
    monkeypatch.setattr(qwen_backend, "create_backend", lambda *args: pytest.fail("alignment must not load ASR"))
    calls = []
    class Aligner:
        def align(self, sample, text, language, sr):
            calls.append((len(sample), text, language, sr))
            return [{"word": text, "start": 0.2, "end": 0.8}]
    monkeypatch.setattr(qwen_backend, "create_aligner", lambda *args: Aligner())
    source = tmp_path / "audio.wav"
    source.write_bytes(b"fixture")
    return source, calls


def test_align_existing_uses_audio_window_offsets_without_asr(monkeypatch, tmp_path):
    source, calls = alignment_fixture(monkeypatch, tmp_path)
    transcript = {"text": "hello there", "language": "en", "segments": [
        {"start": 0, "end": 2, "text": "hello"},
        {"start": 3, "end": 5, "text": "there", "speaker": "S01"},
    ]}
    result = align_transcript.align_existing(source, transcript)
    assert calls == [(32000, "hello", "en", 16000), (32000, "there", "en", 16000)]
    assert result["word_segments"] == [
        {"word": "hello", "start": 0.2, "end": 0.8},
        {"word": "there", "start": 3.2, "end": 3.8, "speaker": "S01"},
    ]
    assert result["metadata"]["timestamp_source"] == "qwen3_forced_alignment"
    assert result["metadata"]["resolved_device"] == "cpu"


def test_alignment_cli_owns_conversion_under_output_parent(monkeypatch, tmp_path):
    source, calls = alignment_fixture(monkeypatch, tmp_path)
    job = tmp_path / "job"
    job.mkdir()
    transcript = tmp_path / "transcript.json"
    transcript.write_text(json.dumps({"text": "hello", "language": "en", "segments": [{"start": 0, "end": 2, "text": "hello"}]}))
    converted = []
    def convert(args, **kwargs):
        wav = Path(args[-1])
        assert wav.parent.parent == job
        wav.write_bytes(b"converted fixture")
        converted.append(wav)
        return types.SimpleNamespace(returncode=0)
    monkeypatch.setattr(align_transcript.subprocess, "run", convert)
    monkeypatch.setattr(sys, "argv", ["align_transcript", "--audio", str(source), "--transcript", str(transcript), "--output", str(job / "aligned.json")])
    align_transcript.main()
    assert converted and not converted[0].parent.exists()
    assert json.loads((job / "aligned.json").read_text())["text"] == "hello"
    assert calls == [(32000, "hello", "en", 16000)]


@pytest.mark.parametrize("segments", [[], [{"text": "hello", "start": 5.1, "end": 5.2}], [{"text": "hello", "start": 1, "end": 1}]])
def test_align_existing_rejects_missing_or_invalid_windows(monkeypatch, tmp_path, segments):
    source, calls = alignment_fixture(monkeypatch, tmp_path)
    with pytest.raises(RecognitionError):
        align_transcript.align_existing(source, {"text": "hello", "segments": segments})
    assert calls == []
