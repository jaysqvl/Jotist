#!/usr/bin/env python3
"""Run one durable WhisperX post-recognition stage."""

import argparse
import gc
import importlib.util
import json
import os
from pathlib import Path
import sys


# Fence an explicit CPU request before any ML package can initialize CUDA.
if (("--device" in sys.argv and sys.argv.index("--device") + 1 < len(sys.argv)
     and sys.argv[sys.argv.index("--device") + 1] == "cpu")
        or "--device=cpu" in sys.argv):
    os.environ["CUDA_VISIBLE_DEVICES"] = ""
os.environ["PYANNOTE_METRICS_ENABLED"] = "false"


_runtime_path = Path(__file__).resolve().with_name("runtime_failure.py")
if not _runtime_path.exists():
    _runtime_path = Path(__file__).resolve().parent.parent / "runtime_failure.py"
_runtime_spec = importlib.util.spec_from_file_location("jotist_runtime_failure", _runtime_path)
_runtime_helper = importlib.util.module_from_spec(_runtime_spec)
_runtime_spec.loader.exec_module(_runtime_helper)
gpu_execution = _runtime_helper.gpu_execution
gpu_failure_kind = _runtime_helper.gpu_failure_kind

_SAFE_EXCEPTION_CLASSES = {
    "RuntimeError", "ValueError", "TypeError", "AttributeError", "KeyError",
    "IndexError", "ImportError", "ModuleNotFoundError", "OSError",
    "FileNotFoundError", "PermissionError", "MemoryError", "GatedRepoError",
    "RepositoryNotFoundError", "HfHubHTTPError",
}


def resolve_device(requested: str) -> str:
    import torch

    if requested not in {"auto", "cpu", "cuda"}:
        raise ValueError("device must be auto, cpu, or cuda")
    if requested == "cuda" and not torch.cuda.is_available():
        raise RuntimeError("CUDA was requested but is not available")
    return ("cuda" if torch.cuda.is_available() else "cpu") if requested == "auto" else requested


def release_cuda() -> None:
    import torch

    gc.collect()
    if torch.cuda.is_available():
        torch.cuda.empty_cache()


def diagnostic_code(exc: Exception, phase: str) -> str:
    chain = []
    current = exc
    while current is not None and len(chain) < 12 and all(current is not item for item in chain):
        chain.append(current)
        current = current.__cause__ or current.__context__
    if any(type(item).__name__ in {"GatedRepoError", "RepositoryNotFoundError", "HfHubHTTPError"}
           or getattr(getattr(item, "response", None), "status_code", None) in {401, 403}
           for item in chain):
        return "runtime_model_access"
    if isinstance(exc, (ImportError, ModuleNotFoundError)):
        return "runtime_dependency_missing"
    if isinstance(exc, (TypeError, AttributeError, KeyError, IndexError)):
        return "runtime_api_incompatible"
    if phase == "alignment" and isinstance(exc, ValueError):
        return "runtime_alignment_error"
    if isinstance(exc, (PermissionError, FileNotFoundError)):
        return "runtime_file_access"
    if isinstance(exc, MemoryError):
        return "runtime_memory_error"
    return "runtime_model_error"


def safe_failure(exc: Exception, phase: str, device: str) -> dict:
    return {
        "diagnostic_code": diagnostic_code(exc, phase),
        "error": "The WhisperX stage failed; inspect its structured phase and exception class.",
        "exception_class": type(exc).__name__ if type(exc).__name__ in _SAFE_EXCEPTION_CLASSES else "ModelError",
        "phase": phase,
        "resolved_device": device if device in {"cpu", "cuda"} else None,
        "gpu_failure_kind": gpu_failure_kind(exc, device),
    }


def run_alignment(args, upstream: dict, device: str, state: dict) -> dict:
    state["phase"] = "model_loading"
    from whisperx import align, load_align_model

    language = upstream.get("language") or args.language or "en"
    segments = upstream.get("segments") or []
    if not segments:
        result = dict(upstream)
        result.setdefault("metadata", {}).update(
            resolved_device=device,
            precision="float32",
            timestamp_source="none",
        )
        return result

    model, metadata = load_align_model(
        language,
        device,
        model_name=args.align_model or None,
        model_dir=args.model_dir or None,
        model_cache_only=args.model_cache_only,
    )
    state["phase"] = "alignment"
    aligned = align(
        segments,
        model,
        metadata,
        args.audio,
        device,
        interpolate_method=args.interpolate_method,
        return_char_alignments=args.return_char_alignments,
        print_progress=False,
    )
    del model
    release_cuda()

    result = dict(upstream)
    result["segments"] = aligned.get("segments", segments)
    result["word_segments"] = aligned.get("word_segments", [])
    result["language"] = language
    result.setdefault("metadata", {}).update(
        resolved_device=device,
        precision="float32",
        timestamp_source="whisperx_alignment",
    )
    return result


def run_diarization(args, upstream: dict, device: str, state: dict) -> dict:
    state["phase"] = "model_loading"
    from whisperx import assign_word_speakers
    from whisperx.diarize import DiarizationPipeline

    pipeline = DiarizationPipeline(
        model_name=args.diarize_model,
        token=os.environ.get("HF_TOKEN") or None,
        device=device,
        cache_dir=args.model_dir or None,
    )
    state["phase"] = "diarization"
    diarization = pipeline(
        args.audio,
        min_speakers=args.min_speakers,
        max_speakers=args.max_speakers,
        return_embeddings=False,
    )
    result = assign_word_speakers(diarization, upstream)
    # JSON checkpoints break the shared object references that WhisperX normally
    # has between per-segment words and the flat word list. Rebuild the flat list
    # so speaker labels survive the durable boundary.
    nested_words = [
        word
        for segment in result.get("segments", [])
        for word in segment.get("words", [])
    ]
    if nested_words:
        result["word_segments"] = nested_words
    del pipeline
    release_cuda()
    result.setdefault("metadata", {}).update(
        resolved_device=device,
        precision="float32",
        speaker_source="whisperx_pyannote",
        diarization_model=args.diarize_model,
    )
    return result


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--stage", required=True, choices=["alignment", "diarization"])
    parser.add_argument("--audio", required=True)
    parser.add_argument("--transcript", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--device", default="auto", choices=["auto", "cpu", "cuda"])
    parser.add_argument("--language", default="en")
    parser.add_argument("--align-model", default="")
    parser.add_argument("--model-dir", default="")
    parser.add_argument("--model-cache-only", action="store_true")
    parser.add_argument("--interpolate-method", default="nearest", choices=["nearest", "linear", "ignore"])
    parser.add_argument("--return-char-alignments", action="store_true")
    parser.add_argument("--diarize-model", default="pyannote/speaker-diarization-community-1")
    parser.add_argument("--min-speakers", type=int)
    parser.add_argument("--max-speakers", type=int)
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    state = {"phase": "runtime_initialization", "device": args.device}
    try:
        device = resolve_device(args.device)
        state["device"] = device
        state["phase"] = "audio_decode"
        upstream = json.loads(Path(args.transcript).read_text(encoding="utf-8"))
        with gpu_execution(device):
            if args.stage == "alignment":
                result = run_alignment(args, upstream, device, state)
            else:
                result = run_diarization(args, upstream, device, state)
        state["phase"] = "output_validation"
        Path(args.output).write_text(json.dumps(result, ensure_ascii=False, allow_nan=False), encoding="utf-8")
    except Exception as exc:
        failure = safe_failure(exc, state["phase"], state["device"])
        Path(args.output).with_name("error.json").write_text(json.dumps(failure), encoding="utf-8")
        print("WhisperX stage failed; inspect the structured diagnostic.", flush=True)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
