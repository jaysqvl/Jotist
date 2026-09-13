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
from qwen_backend import create_aligner, create_backend, restore_alignment_surface, validate_alignment_language
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
            return types.SimpleNamespace(logits=[1])
        def generate(self, **kwargs):
            return np.array([[1, 2, 99]])

    monkeypatch.setitem(sys.modules, "torch", types.SimpleNamespace(inference_mode=contextlib.nullcontext))
    monkeypatch.setitem(sys.modules, "transformers", types.SimpleNamespace(AutoModelForTokenClassification=Model, AutoModelForMultimodalLM=Model, AutoProcessor=Processor))
    recognized = create_backend("fixture", "cpu", "float32", {"language": "en"}).transcribe(np.ones(16000))
    assert recognized == {"text": "C++ foo.bar().", "language": "English"}
    result = create_aligner("cpu", "float32", {}).align(np.ones(16000), "C++ foo.bar().", "en")
    assert result == [{"word": "C++", "start": 0.1, "end": 0.4},
                      {"word": "foo.bar().", "start": 0.5, "end": 0.9}]
