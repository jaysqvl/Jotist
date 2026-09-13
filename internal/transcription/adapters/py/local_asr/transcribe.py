#!/usr/bin/env python3
"""Scriberr local recognition contract; no model download occurs on import."""
from __future__ import annotations

import argparse
import gc
import json
import math
import os
from pathlib import Path
import subprocess
import tempfile

from backends import RecognitionError, create_backend


# Load the bundled helper by path, including under Python isolated mode.
import importlib.util as _runtime_import
from pathlib import Path as _RuntimePath
_runtime_path = _RuntimePath(__file__).resolve().with_name("runtime_failure.py")
if not _runtime_path.exists():
    _runtime_path = _RuntimePath(__file__).resolve().parent.parent / "runtime_failure.py"
_runtime_spec = _runtime_import.spec_from_file_location("scriberr_runtime_failure", _runtime_path)
_runtime_helper = _runtime_import.module_from_spec(_runtime_spec)
_runtime_spec.loader.exec_module(_runtime_helper)
gpu_execution = _runtime_helper.gpu_execution


def resolve_compute(device, precision, torch):
    if device not in {"cpu", "cuda", "auto"}:
        raise RecognitionError("Unsupported inference device.")
    if precision not in {"float32", "float16", "bfloat16"}:
        raise RecognitionError("Unsupported inference precision.")
    if device == "auto":
        device = "cuda" if torch.cuda.is_available() else "cpu"
        if device == "cpu":
            precision = "float32"
    if device == "cuda" and not torch.cuda.is_available():
        raise RecognitionError("CUDA was explicitly requested but is unavailable; choose CPU or install a compatible CUDA runtime.")
    if device == "cpu" and precision != "float32":
        raise RecognitionError("CPU inference requires float32 precision.")
    return device, getattr(torch, precision)


def load_config(path):
    config = json.loads(Path(path).read_text(encoding="utf-8"))
    catalog = json.loads(Path(__file__).with_name("models.json").read_text(encoding="utf-8"))
    spec = next((entry for entry in catalog if entry["id"] == config.get("model_id")), None)
    if spec is None:
        raise RecognitionError("Model is not present in the local recognition catalog.")
    if config.get("context", "").strip() and spec["context_mode"] != "prose_and_terms":
        raise RecognitionError("Selected model does not support prose context.")
    if config.get("context_terms", "").strip() and spec["context_mode"] == "none":
        raise RecognitionError("Selected model does not support vocabulary hints.")
    if "hf_token" in config:
        raise RecognitionError("Credentials must be supplied through HF_TOKEN, never a request file.")
    # Untrusted request fields cannot override the code revision or engine.
    config.update(spec)
    if (config.get("external_diarization_requested") and not config.get("align_words", not spec.get("native_timestamps"))
            and not spec.get("native_timestamps") and spec["engine"] != "granite_plus"):
        raise RecognitionError("This model requires word alignment for external diarization; enable align_words.")
    if config.get("align_words", not spec.get("native_timestamps")) and spec["engine"] != "granite_plus":
        from qwen_backend import validate_alignment_language
        language = config.get("language", "en")
        # Qwen ASR returns a detected language; other backends currently return
        # only the configured hint, so literal auto cannot reach the aligner.
        if spec["engine"] != "qwen" or str(language or "").lower() not in {"", "auto"}:
            validate_alignment_language(language)
    for name in ("context", "context_terms"):
        if len(config.get(name, "").encode("utf-8")) > 32768:
            raise RecognitionError("Recognition context exceeds 32768 bytes.")
    return config


def audio_windows(audio, sample_rate, seconds, max_seconds):
    """Contiguous windows snapped toward quiet boundaries; no gaps or repeats."""
    import numpy as np
    if seconds == 0:
        if len(audio) / sample_rate > max_seconds:
            raise RecognitionError("Recording exceeds this model's whole-recording limit; choose a smaller chunk_duration.")
        return [(0, len(audio))]
    if seconds < 1 or seconds > max_seconds:
        raise RecognitionError("Audio window is outside the model's supported range.")
    windows, start = [], 0
    cap = int(seconds * sample_rate)
    while start < len(audio):
        end = min(len(audio), start + cap)
        if end < len(audio) and cap > sample_rate * 3:
            # Search the final second for the quietest 20ms frame. This is a
            # boundary heuristic, not claimed to be learned VAD/word alignment.
            lower, frame = max(start + cap // 2, end - sample_rate), max(1, sample_rate // 50)
            choices = list(range(lower, end - frame + 1, frame))
            if choices:
                end = min(choices, key=lambda i: float(np.mean(audio[i:i + frame] ** 2))) + frame // 2
        windows.append((start, end))
        start = end
    return windows


def validate_times(items, duration):
    for item in items:
        start, end = item.get("start"), item.get("end")
        if not isinstance(start, (int, float)) or not isinstance(end, (int, float)):
            raise RecognitionError("Model output lacks numeric timestamps.")
        if not math.isfinite(start) or not math.isfinite(end) or start < 0 or end < start or start > duration or end > duration + 0.25:
            raise RecognitionError("Model output has timestamps outside its audio window.")
        item["end"] = min(float(end), duration)


def word_segments(words):
    """A segment per aligned word prevents a long ASR chunk spanning speakers."""
    return [{"start":w["start"], "end":w["end"], "text":w["word"], **({"speaker":w["speaker"]} if w.get("speaker") else {})} for w in words]


def assign_native_speakers(words, segments):
    for word in words:
        # Words aligned inside a specific native speaker segment already have
        # stronger attribution than overlap heuristics, especially when two
        # speakers talk simultaneously.
        if word.get("speaker"):
            continue
        overlaps = [(max(0, min(word["end"], s["end"]) - max(word["start"], s["start"])), s) for s in segments]
        if overlaps:
            overlap, segment = max(overlaps, key=lambda pair: pair[0])
            if overlap > 0 and segment.get("speaker"):
                word["speaker"] = segment["speaker"]


def execute(config):
    import numpy as np
    import soundfile as sf
    import torch

    device, dtype = resolve_compute(config.get("device", "cpu"), config.get("precision", "float32"), torch)
    config["_resolved_device"] = device
    if device == "cpu":
        os.environ["CUDA_VISIBLE_DEVICES"] = ""
    if device == "cuda":
        # Float32 remains float32 instead of silently enabling TF32 arithmetic.
        torch.backends.cuda.matmul.allow_tf32 = False
        torch.backends.cudnn.allow_tf32 = False
    audio_path = Path(config["audio_file"])
    if not audio_path.is_file():
        raise RecognitionError("Audio input file is missing.")
    # The Go worker removes this job directory even when SIGKILL prevents
    # Python's context-manager cleanup from running during conversion.
    temp_parent = Path(config["output"]).parent if config.get("output") else None
    with tempfile.TemporaryDirectory(prefix="scriberr-local-asr-", dir=temp_parent) as tmp:
        wav = str(Path(tmp) / "audio.wav")
        converted = subprocess.run(["ffmpeg", "-nostdin", "-v", "error", "-i", str(audio_path), "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_f32le", wav], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if converted.returncode:
            raise RecognitionError("FFmpeg could not decode the audio input.")
        audio, sr = sf.read(wav, dtype="float32")
    if audio.ndim != 1 or not len(audio) or not np.isfinite(audio).all():
        raise RecognitionError("Input audio is empty or invalid.")
    duration = len(audio) / sr
    window = int(config.get("chunk_duration", config["default_chunk_seconds"]))
    if window == 0 and not config.get("native_speakers"):
        window = config["default_chunk_seconds"]
    bounds = audio_windows(audio, sr, window, config["max_chunk_seconds"])
    backend = model_call(config, device, create_backend, config["model_id"], device, dtype, config)
    chunks = []
    for index, (start, end) in enumerate(bounds):
        sample = audio[start:end]
        # Exact digital silence cannot contain speech. Do not suppress quiet
        # real voices using an arbitrary amplitude/noise threshold.
        result = model_call(config, device, backend.transcribe, sample, sr) if np.any(sample) else {"text":"", "language":config.get("language", "en")}
        # Reject invalid native times before they can be used to crop audio for
        # forced alignment, including segments starting beyond the recording.
        validate_times(result.get("segments", []), len(sample) / sr)
        validate_times(result.get("word_segments", []), len(sample) / sr)
        result["start_sample"], result["end_sample"] = start, end
        if config.get("native_speakers") and len(bounds)>1:
            for segment in result.get("segments", []):
                if segment.get("speaker"):
                    segment["speaker"] = f"chunk_{index + 1}_{segment['speaker']}"
        chunks.append(result)
        print(f"Recognition window {index + 1}/{len(bounds)} complete", flush=True)

    # Model memory is released before loading a separate forced aligner. This
    # keeps CPU RAM/GPU VRAM closer to the largest model rather than their sum.
    del backend
    gc.collect()
    if device == "cuda":
        model_call(config, device, torch.cuda.empty_cache)
    aligner = None
    try:
        if config.get("align_words", not config.get("native_timestamps")) and any(c.get("text", "").strip() and not c.get("word_segments") for c in chunks):
            from qwen_backend import create_aligner, validate_alignment_language
            for chunk in chunks:
                if chunk.get("text", "").strip() and not chunk.get("word_segments"):
                    validate_alignment_language(chunk.get("language") or config.get("language", "en"))
            aligner = model_call(config, device, create_aligner, device, dtype, config)
        for chunk in chunks:
            if aligner is not None and chunk.get("text", "").strip() and not chunk.get("word_segments"):
                sample = audio[chunk["start_sample"]:chunk["end_sample"]]
                # Align each native speaker segment separately to retain native
                # speaker attribution while bounding the aligner's input size.
                if chunk.get("segments"):
                    words = []
                    for segment in chunk["segments"]:
                        segment_start = max(0, int(segment["start"] * sr))
                        segment_end = min(len(sample), int(segment["end"] * sr))
                        aligned = model_call(config, device, aligner.align, sample[segment_start:segment_end], segment["text"], chunk.get("language", config.get("language", "en")), sr)
                        for word in aligned:
                            word["start"] += segment_start / sr
                            word["end"] += segment_start / sr
                            if segment.get("speaker"):
                                word["speaker"] = segment["speaker"]
                        words.extend(aligned)
                else:
                    words = model_call(config, device, aligner.align, sample, chunk["text"], chunk.get("language", config.get("language", "en")), sr)
                if not words:
                    raise RecognitionError("Forced alignment returned no words for a nonempty transcript.")
                chunk["word_segments"] = words
                chunk["timestamp_source"] = "qwen3_forced_alignment"
    finally:
        del aligner
        gc.collect()

    segments, words, texts, sources = [], [], [], set()
    for chunk in chunks:
        text = chunk.get("text", "").strip()
        if not text:
            continue
        texts.append(text)
        offset = chunk["start_sample"] / sr
        chunk_duration = (chunk["end_sample"] - chunk["start_sample"]) / sr
        chunk_words = chunk.get("word_segments", [])
        chunk_segments = chunk.get("segments", [])
        validate_times(chunk_words, chunk_duration)
        validate_times(chunk_segments, chunk_duration)
        if chunk_words:
            if chunk_segments:
                assign_native_speakers(chunk_words, chunk_segments)
            chunk_segments = word_segments(chunk_words)
            sources.add(chunk.get("timestamp_source", "native_word_timestamps"))
        elif chunk_segments:
            sources.add(chunk.get("timestamp_source", "native_segments"))
        else:
            # Honest coarse bounds only when alignment is explicitly disabled.
            chunk_segments = [{"start":0.0, "end":chunk_duration, "text":text}]
            sources.add("audio_window_bounds_unaligned")
        for item in chunk_words:
            words.append({**item, "start":item["start"] + offset, "end":item["end"] + offset})
        for item in chunk_segments:
            segments.append({**item, "start":item["start"] + offset, "end":item["end"] + offset})
    metadata = {"resolved_device":device, "precision":str(dtype).replace("torch.", ""), "context_mode":config["context_mode"], "timestamp_source":",".join(sorted(sources)) or "none", "duration_seconds":str(duration), "chunk_count":str(len(bounds))}
    keep_native_speakers = config.get("diarize") is True and config.get("diarize_model") == "native"
    if not keep_native_speakers:
        for item in segments + words:
            item.pop("speaker", None)
    if config.get("native_speakers") and keep_native_speakers:
        metadata["speaker_scope"] = "recording" if len(bounds)==1 else "chunk"
    if config.get("revision"):
        metadata["model_revision"] = config["revision"]
    language = next((chunk.get("language") for chunk in chunks if chunk.get("text", "").strip() and chunk.get("language")), config.get("language", "en"))
    return {"text":" ".join(texts), "language":language, "segments":segments, "word_segments":words, "model_used":config["model_id"], "metadata":metadata}


def gpu_failure_kind(exc, resolved_device, execution=False):
    if not execution or isinstance(exc, RecognitionError):
        return None
    return _runtime_helper.gpu_failure_kind(exc, resolved_device)


def model_call(config, device, function, *args, **kwargs):
    config["_gpu_execution"] = True
    value = function(*args, **kwargs)
    config["_gpu_execution"] = False
    return value


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    args = parser.parse_args()
    request_path = Path(args.config)
    config = {}
    try:
        config = load_config(request_path)
        result = execute(config)
        output = Path(config["output"])
        temporary = output.with_suffix(".partial")
        temporary.write_text(json.dumps(result, ensure_ascii=False, allow_nan=False), encoding="utf-8")
        os.chmod(temporary, 0o600)
        temporary.replace(output)
    except Exception as exc:
        # Third-party errors can contain authenticated URLs, prompts and audio
        # paths. Return safe application messages or only the exception class.
        message = str(exc) if isinstance(exc, RecognitionError) else f"{type(exc).__name__} while loading or running the model; verify model access and installed runtime."
        for key in ("HF_TOKEN", "HUGGING_FACE_HUB_TOKEN"):
            if os.environ.get(key):
                message = message.replace(os.environ[key], "[redacted]")
        failure = request_path.with_name("error.json")
        failure.write_text(json.dumps({"error": message, "resolved_device": config.get("_resolved_device"), "gpu_failure_kind": gpu_failure_kind(exc, config.get("_resolved_device"), config.get("_gpu_execution", False))}), encoding="utf-8")
        os.chmod(failure, 0o600)
        print(message, flush=True)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
