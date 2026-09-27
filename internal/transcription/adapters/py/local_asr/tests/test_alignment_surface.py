"""Preserve recognition text when Qwen's alignment tokenizer removes symbols."""
import contextlib
from pathlib import Path
import sys
import types

import numpy as np
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from backends import RecognitionError
import qwen_backend
from qwen_backend import bounded_alignment_logits, create_aligner, create_backend, normalize_alignment, restore_alignment_surface, validate_alignment_language
from transcribe import word_segments


def test_alignment_preserves_technical_tokens_and_punctuation():
    words = [{"word": word, "start": index, "end": index + 0.8}
             for index, word in enumerate(["Use", "C", "foobar", "v123"])]
    restored = restore_alignment_surface(words, 'Use "C++", foo.bar(); v1.2.3.')
    assert [word["word"] for word in restored] == ["Use", '"C++",', "foo.bar();", "v1.2.3."]
    assert [(word["start"], word["end"]) for word in restored] == [(word["start"], word["end"]) for word in words]
    assert words[1]["word"] == "C", "restoration must not mutate alignment input"


@pytest.mark.parametrize("words,text", [
    (["Deploy"], "Deploy foo.bar()."),
    (["Deploy", "other"], "Deploy foo.bar()."),
    (["wear", "e"], "we are"),
    (["hello", "hello"], "hello"),
])
def test_incomplete_or_invented_alignment_mapping_fails(words, text):
    with pytest.raises(RecognitionError):
        restore_alignment_surface([{"word": word, "start": 0, "end": 1} for word in words], text)


def test_surface_restoration_preserves_overlapping_native_speakers():
    first = restore_alignment_surface([{"word": "C", "start": 0.1, "end": 0.8, "speaker": "S01"}], "C++.")
    second = restore_alignment_surface([{"word": "foobar", "start": 0.5, "end": 1.2, "speaker": "S02"}], "foo.bar()?")
    assert word_segments(first + second) == [
        {"text": "C++.", "start": 0.1, "end": 0.8, "speaker": "S01"},
        {"text": "foo.bar()?", "start": 0.5, "end": 1.2, "speaker": "S02"},
    ]


@pytest.mark.parametrize("language", ["auto", "", None])
def test_unresolved_alignment_language_is_actionable(language):
    with pytest.raises(RecognitionError, match="Select an explicit supported language or disable word alignment"):
        validate_alignment_language(language)


@pytest.mark.parametrize("language", ["ar", "Arabic", "hi", "Hindi"])
def test_asr_only_language_is_not_advertised_as_alignable(language):
    with pytest.raises(RecognitionError, match="Disable word alignment for ASR-only"):
        validate_alignment_language(language)


def test_alignment_checks_optional_tokenizers(monkeypatch):
    monkeypatch.setattr(qwen_backend, "find_spec", lambda name: None)
    for language, dependency in [("ja", "nagisa"), ("Korean", "soynlp")]:
        with pytest.raises(RecognitionError, match=dependency):
            validate_alignment_language(language)
    assert validate_alignment_language("en") == "English"
    monkeypatch.setattr(qwen_backend, "find_spec", lambda name: object())
    assert validate_alignment_language("ja") == "Japanese"


def test_shared_qwen_backend_and_aligner_preserve_surface_and_processor_kwargs(monkeypatch):
    class Inputs(dict):
        def to(self, *args):
            return self

    class Processor:
        timestamp_segment_time = 80
        @classmethod
        def from_pretrained(cls, *args, **kwargs):
            return cls()
        def prepare_forced_aligner_inputs(self, **kwargs):
            assert kwargs["transcript"] == "C++ foo.bar()."
            assert kwargs["processor_kwargs"] == {"audio_kwargs": {"sampling_rate": 16000}}
            assert "audio_kwargs" not in kwargs
            return Inputs(input_ids=[1]), [["C", "foobar"]]
        def apply_transcription_request(self, **kwargs):
            assert kwargs["processor_kwargs"] == {"audio_kwargs": {"sampling_rate": 16000}}
            assert "audio_kwargs" not in kwargs
            return Inputs(input_ids=np.array([[1]]))
        def decode(self, *args, **kwargs):
            return [{"transcription": "C++ foo.bar().", "language": "English"}]
        def decode_forced_alignment(self, **kwargs):
            assert kwargs["logits"].shape[-1] == 13, "one second admits timestamp classes 0 through 12"
            return [[{"text": "C", "start_time": 0.1, "end_time": 0.4},
                     {"text": "foobar", "start_time": 0.5, "end_time": 0.9}]]

    class Model:
        device, dtype = "cpu", "float32"
        config = types.SimpleNamespace(timestamp_token_id=1)
        generation_config = types.SimpleNamespace(eos_token_id=99)
        @classmethod
        def from_pretrained(cls, *args, **kwargs):
            return cls()
        def to(self, *args):
            return self
        def eval(self):
            return self
        def __call__(self, **kwargs):
            return types.SimpleNamespace(logits=np.zeros((1, 1, 3750)))
        def generate(self, **kwargs):
            return np.array([[1, 2, 99]])

    monkeypatch.setitem(sys.modules, "torch", types.SimpleNamespace(inference_mode=contextlib.nullcontext))
    monkeypatch.setitem(sys.modules, "transformers", types.SimpleNamespace(AutoModelForTokenClassification=Model, AutoModelForMultimodalLM=Model, AutoProcessor=Processor))
    recognized = create_backend("fixture", "cpu", "float32", {"language": "en"}).transcribe(np.ones(16000))
    assert recognized == {"text": "C++ foo.bar().", "language": "English"}
    result = create_aligner("cpu", "float32", {}).align(np.ones(16000), "C++ foo.bar().", "en")
    assert result == [{"word": "C++", "start": 0.1, "end": 0.4},
                      {"word": "foo.bar().", "start": 0.5, "end": 0.9}]


def test_alignment_selects_best_valid_time_at_a_real_chunk_boundary():
    # A 29.41s window produced a 29.68s end for its final word. The old
    # post-decode tolerance rejected the entire recording by 20ms.
    scores = np.full((1, 2, 3750), -10.0)
    scores[0, 0, 365] = 10.0  # 29.20s start, inside the audio
    scores[0, 1, 371] = 12.0  # 29.68s end, physically impossible
    scores[0, 1, 367] = 11.0  # 29.36s, the best valid end prediction
    before = scores.copy()
    assert scores.argmax(axis=-1).tolist() == [[365, 371]]
    decoded = bounded_alignment_logits(scores, 29.41, 80).argmax(axis=-1)[0] * 0.08
    assert normalize_alignment([{"text": "word", "start_time": decoded[0], "end_time": decoded[1]}], 29.41) == [
        {"word": "word", "start": 29.2, "end": 29.36},
    ]
    np.testing.assert_array_equal(scores, before)


@pytest.mark.parametrize("duration,interval", [(0.02, 80), (1, 80), (29.41, 80), (30, 80), (300, 80), (1, 20)])
def test_alignment_timestamp_classes_never_exceed_supplied_audio(duration, interval):
    scores = np.arange(20000).reshape(1, 1, -1)
    bounded = bounded_alignment_logits(scores, duration, interval)
    predicted = int(bounded.argmax(axis=-1)[0, 0])
    assert predicted * interval / 1000 <= duration
    assert (predicted + 1) * interval / 1000 > duration


@pytest.mark.parametrize("duration,interval", [(0, 80), (-1, 80), (float("nan"), 80), (1, 0), (1, float("inf"))])
def test_alignment_rejects_invalid_time_grid(duration, interval):
    with pytest.raises(RecognitionError, match="positive audio duration"):
        bounded_alignment_logits(np.zeros((1, 2, 3)), duration, interval)
