"""Contract/pipeline tests. No checkpoint downloads or inference claims."""
import contextlib
import importlib
import json
from pathlib import Path
import sys
import types
import weakref

import numpy as np
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from backends import RecognitionError, ensure_generation_complete, parse_granite_timestamps, parse_moss, vocabulary
import transcribe
from qwen_backend import context_prompt, normalize_alignment


def test_parsers_preserve_native_timing_and_speaker_changes():
    parts = parse_moss("[0.48][S01]Hello[1.66][2.26][S02]Deployment ready[3.81]")
    assert [(p["speaker"], p["start"], p["end"]) for p in parts] == [("S01", 0.48, 1.66), ("S02", 2.26, 3.81)]
    with pytest.raises(RecognitionError):
        parse_moss("[0.48][S01]Hello[1.66] unfinished")
    with pytest.raises(RecognitionError):
        parse_moss("[3][S01]Hello[1]")
    words = parse_granite_timestamps("_ [T:950] ship [T:980] it [T:20] _ [T:45] today [T:90]")
    assert words == [{"word":"ship", "start":9.5, "end":9.8}, {"word":"it", "start":9.8, "end":10.2}, {"word":"today", "start":10.45, "end":10.9}]


def test_generation_cutoff_is_not_success():
    ensure_generation_complete([1,2,99], 3, [99])
    with pytest.raises(RecognitionError):
        ensure_generation_complete([1,2,3], 3, [99])


def test_overlapping_native_speakers_keep_segment_attribution():
    words = [{"word": "yes", "start": 1.2, "end": 1.8, "speaker": "S02"},
             {"word": "hello", "start": 0.1, "end": 0.5}]
    segments = [{"text": "hello", "start": 0.0, "end": 4.0, "speaker": "S01"},
                {"text": "yes", "start": 1.0, "end": 2.0, "speaker": "S02"}]
    transcribe.assign_native_speakers(words, segments)
    assert words[0]["speaker"] == "S02"
    assert words[1]["speaker"] == "S01"


def test_context_policies(tmp_path):
    config = {"model_id":"ibm-granite/granite-speech-4.1-2b", "context":"rewrite as a summary"}
    path = tmp_path / "request.json"
    path.write_text(json.dumps(config))
    with pytest.raises(RecognitionError):
        transcribe.load_config(path)
    config.pop("context")
    config["context_terms"] = "PostgreSQL\nScriberr\nPostgreSQL"
    config["revision"] = "attacker-controlled-code"
    path.write_text(json.dumps(config))
    assert vocabulary(config) == ["PostgreSQL", "Scriberr"]
    config["model_id"] = "Edge0/ARK-ASR-3B"
    config.pop("context_terms")
    path.write_text(json.dumps(config))
    assert transcribe.load_config(path)["revision"] == "1e28271b79edc97635783bea65abc89195a09ed3"
    config["hf_token"] = "never-persist"
    path.write_text(json.dumps(config))
    with pytest.raises(RecognitionError):
        transcribe.load_config(path)


def test_external_diarization_requires_usable_timestamps(tmp_path):
    path = tmp_path / "request.json"
    for model, allowed in (("Qwen/Qwen3-ASR-1.7B-hf", False),
                           ("ibm-granite/granite-speech-4.1-2b-plus", True),
                           ("OpenMOSS-Team/MOSS-Transcribe-Diarize", True)):
        path.write_text(json.dumps({"model_id": model, "align_words": False, "external_diarization_requested": True}))
        if allowed:
            assert transcribe.load_config(path)["external_diarization_requested"]
        else:
            with pytest.raises(RecognitionError, match="requires word alignment"):
                transcribe.load_config(path)


def test_alignment_language_preflight_precedes_recognition(tmp_path):
    path = tmp_path / "request.json"
    for model, language in (("mistralai/Voxtral-Mini-3B-2507", "auto"), ("Qwen/Qwen3-ASR-1.7B-hf", "Arabic")):
        request = {"model_id": model, "language": language, "align_words": True}
        path.write_text(json.dumps(request))
        with pytest.raises(RecognitionError, match="word alignment"):
            transcribe.load_config(path)
        request["align_words"] = False
        path.write_text(json.dumps(request))
        assert transcribe.load_config(path)["language"] == language
    path.write_text(json.dumps({"model_id": "Qwen/Qwen3-ASR-1.7B-hf", "language": "auto", "align_words": True}))
    assert transcribe.load_config(path)["language"] == "auto", "Qwen ASR supplies a detected language before alignment"


def test_compute_is_explicit_and_cpu_precision_is_preserved():
    torch = types.SimpleNamespace(cuda=types.SimpleNamespace(is_available=lambda:True), float32="fp32", float16="fp16", bfloat16="bf16")
    assert transcribe.resolve_compute("cpu", "float32", torch) == ("cpu", "fp32")
    assert transcribe.resolve_compute("auto", "float32", torch) == ("cuda", "fp32")
    assert transcribe.resolve_compute("auto", "float16", torch) == ("cuda", "fp16")
    with pytest.raises(RecognitionError):
        transcribe.resolve_compute("cpu", "float16", torch)
    torch.cuda.is_available = lambda:False
    assert transcribe.resolve_compute("auto", "float16", torch) == ("cpu", "fp32")
    with pytest.raises(RecognitionError):
        transcribe.resolve_compute("auto", "invalid", torch)
    with pytest.raises(RecognitionError):
        transcribe.resolve_compute("cuda", "float32", torch)


def test_gpu_failure_sidecar_classification_is_scoped_and_safe():
    runtime = RuntimeError("unsupported backend dtype; private-input")
    assert transcribe.gpu_failure_kind(runtime, "cuda", True) == "cuda_runtime_error"
    assert transcribe.gpu_failure_kind(runtime, "cuda", False) is None
    assert transcribe.gpu_failure_kind(runtime, "cpu", True) is None
    assert transcribe.gpu_failure_kind(RecognitionError("CUDA out of memory"), "cuda", True) is None
    assert transcribe.gpu_failure_kind(RuntimeError("403 Client Error Forbidden"), "cuda", True) is None
    config = {}
    with pytest.raises(RuntimeError):
        transcribe.model_call(config, "cuda", lambda: (_ for _ in ()).throw(runtime))
    assert config["_gpu_execution"] is True
    assert transcribe.model_call(config, "cuda", lambda: "done") == "done"
    assert config["_gpu_execution"] is False


def test_windows_have_no_dropped_or_repeated_samples():
    audio = np.ones(16000 * 72, dtype=np.float32)
    audio[16000*29:16000*29+500] = 0
    bounds = transcribe.audio_windows(audio,16000,30,30)
    assert bounds[0][0] == 0 and bounds[-1][1] == len(audio)
    assert all(a[1] == b[0] for a,b in zip(bounds,bounds[1:]))
    assert all(0 < end-start <= 30*16000 for start,end in bounds)
    with pytest.raises(RecognitionError):
        transcribe.audio_windows(audio,16000,0,30)


def test_qwen_context_and_alignment_contract():
    assert context_prompt({"context":"Release planning", "context_terms":"PostgreSQL\nRedis"}) == "Release planning\nVocabulary: PostgreSQL, Redis."
    assert normalize_alignment([{"text":"hello", "start_time":0.5, "end_time":0.8}], 1) == [{"word":"hello", "start":0.5, "end":0.8}]
    with pytest.raises(ValueError):
        normalize_alignment([{"text":"bad", "start_time":0.5, "end_time":5}],1)


def test_conversion_audio_is_owned_by_the_job_output_directory(monkeypatch, tmp_path):
    source = tmp_path / "source.wav"
    source.write_bytes(b"fixture")
    job = tmp_path / "job"
    job.mkdir()
    monkeypatch.setitem(sys.modules, "soundfile", types.SimpleNamespace())
    monkeypatch.setitem(sys.modules, "torch", types.SimpleNamespace(float32="torch.float32"))
    converted = []
    def stop_during_conversion(args, **kwargs):
        wav = Path(args[-1])
        assert wav.parent.parent == job
        wav.write_bytes(b"converted fixture")
        converted.append(wav)
        raise RecognitionError("stop the conversion fixture")
    monkeypatch.setattr(transcribe.subprocess, "run", stop_during_conversion)
    with pytest.raises(RecognitionError, match="stop the conversion fixture"):
        transcribe.execute({"audio_file": str(source), "output": str(job / "result.json")})
    assert converted and not converted[0].parent.exists()
    assert job.exists() and source.exists()


def setup_pipeline(monkeypatch, tmp_path, response, align_words, native=False):
    audio = np.ones(16000 * 2, dtype=np.float32)
    monkeypatch.setitem(sys.modules,"soundfile",types.SimpleNamespace(read=lambda *args,**kwargs:(audio,16000)))
    torch = types.SimpleNamespace(cuda=types.SimpleNamespace(is_available=lambda:False), float32="torch.float32")
    monkeypatch.setitem(sys.modules,"torch",torch)
    monkeypatch.setattr(transcribe.subprocess,"run",lambda *args,**kwargs:types.SimpleNamespace(returncode=0))
    input_file=tmp_path/"source.wav";input_file.write_bytes(b"fixture")
    refs=[]
    class Backend:
        def transcribe(self,*args): return dict(response)
    def factory(*args):
        obj=Backend();refs.append(weakref.ref(obj));return obj
    monkeypatch.setattr(transcribe,"create_backend",factory)
    class Aligner:
        def align(self,audio,text,language,sample_rate=16000):
            assert refs[0]() is None, "ASR model remains resident during alignment"
            return [{"word":"hello", "start":0.25, "end":0.5}]
    monkeypatch.setattr(importlib.import_module("qwen_backend"),"create_aligner",lambda *args:Aligner())
    config={"model_id":"fixture", "audio_file":str(input_file), "device":"cpu", "precision":"float32", "context_mode":"none", "default_chunk_seconds":30, "max_chunk_seconds":30, "align_words":align_words, "language":"en"}
    config.update(diarize=native,diarize_model="native" if native else "none")
    return transcribe.execute(config)


def test_text_only_alignment_releases_model_and_creates_truthful_word_segments(monkeypatch,tmp_path):
    result=setup_pipeline(monkeypatch,tmp_path,{"text":"hello", "language":"en"},True)
    assert result["segments"] == [{"text":"hello", "start":0.25, "end":0.5}]
    assert result["metadata"]["timestamp_source"] == "qwen3_forced_alignment"
    assert result["metadata"]["resolved_device"] == "cpu"


def test_disabled_alignment_reports_only_audio_window_bounds(monkeypatch,tmp_path):
    result=setup_pipeline(monkeypatch,tmp_path,{"text":"hello", "language":"en"},False)
    assert result["word_segments"] == []
    assert result["segments"][0]["end"] == 2
    assert result["metadata"]["timestamp_source"] == "audio_window_bounds_unaligned"


def test_native_speakers_survive_word_alignment(monkeypatch,tmp_path):
    response={"text":"hello", "language":"en", "segments":[{"text":"hello", "start":0.5, "end":1.5, "speaker":"S02"}]}
    result=setup_pipeline(monkeypatch,tmp_path,response,True,native=True)
    assert result["word_segments"][0]["speaker"] == "S02"
    assert result["word_segments"][0]["start"] == 0.75


def test_native_speakers_are_omitted_unless_requested(monkeypatch,tmp_path):
    response={"text":"hello", "language":"en", "segments":[{"text":"hello", "start":0.5, "end":1.5, "speaker":"S02"}]}
    result=setup_pipeline(monkeypatch,tmp_path,response,True,native=False)
    assert "speaker" not in result["word_segments"][0]
    assert "speaker" not in result["segments"][0]


def test_out_of_window_native_timestamps_fail(monkeypatch,tmp_path):
    response={"text":"hello", "language":"en", "segments":[{"text":"hello", "start":0.5, "end":20, "speaker":"S02"}]}
    with pytest.raises(RecognitionError):
        setup_pipeline(monkeypatch,tmp_path,response,False)
