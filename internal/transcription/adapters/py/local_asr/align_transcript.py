"""Align an existing chunked transcript without loading any ASR checkpoint."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

from backends import RecognitionError
from diagnostics import safe_failure
from transcribe import gpu_failure_kind, model_call, resolve_compute, validate_times, word_segments


def align_existing(audio_path, transcript, device="cpu", precision="float32", language="en", temp_parent=None, diagnostic_state=None):
    import numpy as np
    import soundfile as sf
    import torch
    from qwen_backend import ALIGNER_REVISION, create_aligner, has_alignable_text, validate_alignment_language

    state = diagnostic_state if diagnostic_state is not None else {}
    state["_diagnostic_phase"] = "runtime_initialization"
    device, dtype = resolve_compute(device, precision, torch)
    state["_resolved_device"] = device
    if device == "cpu":
        os.environ["CUDA_VISIBLE_DEVICES"] = ""
    else:
        torch.backends.cuda.matmul.allow_tf32 = False
        torch.backends.cudnn.allow_tf32 = False
    state["_diagnostic_phase"] = "audio_decode"
    if not Path(audio_path).is_file():
        raise RecognitionError("Audio input file is missing.")
    # CLI callers supply the Go-owned job directory so forced termination
    # cannot strand a converted recording outside the caller's cleanup scope.
    with tempfile.TemporaryDirectory(prefix="scriberr-alignment-", dir=temp_parent) as tmp:
        wav = str(Path(tmp) / "audio.wav")
        converted = subprocess.run(
            ["ffmpeg", "-nostdin", "-v", "error", "-i", str(audio_path), "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_f32le", wav],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        if converted.returncode:
            raise RecognitionError("FFmpeg could not decode the audio input.")
        audio, sr = sf.read(wav, dtype="float32")
    if audio.ndim != 1 or not len(audio) or not np.isfinite(audio).all():
        raise RecognitionError("Input audio is empty or invalid.")
    duration = len(audio) / sr
    segments = transcript.get("segments", [])
    if not isinstance(segments, list):
        raise RecognitionError("Alignment requires transcript segments with audio window bounds.")
    validate_times(segments, duration)
    text = transcript.get("text", "").strip()
    if text and not segments:
        raise RecognitionError("Alignment requires transcript segments with audio window bounds.")
    for segment in segments:
        if segment["end"] - segment["start"] > 300:
            raise RecognitionError("Alignment audio windows must not exceed 300 seconds.")
        if segment.get("text", "").strip() and segment["end"] <= segment["start"]:
            raise RecognitionError("Nonempty transcript segments need a positive audio duration.")
    language = transcript.get("language") or language
    words, coarse_segments = [], []
    alignable = [segment for segment in segments if has_alignable_text(segment.get("text"))]
    if alignable:
        validate_alignment_language(language)
        # Recognition happened in the caller. Only the alignment model is loaded.
        state["_diagnostic_phase"] = "model_loading"
        aligner = model_call(state, device, create_aligner, device, dtype, {"language": language})
        state["_diagnostic_phase"] = "alignment"
        for segment in segments:
            segment_text = segment.get("text", "").strip()
            if not segment_text:
                continue
            if not has_alignable_text(segment_text):
                coarse_segments.append({key: value for key, value in segment.items() if key in {"start", "end", "text", "speaker"}})
                continue
            start, end = int(segment["start"] * sr), int(segment["end"] * sr)
            sample = audio[start:end]
            aligned = model_call(state, device, aligner.align, sample, segment_text, language, sr)
            if not aligned:
                raise RecognitionError("Forced alignment returned no words for a nonempty transcript.")
            validate_times(aligned, len(sample) / sr)
            for word in aligned:
                word["start"] += start / sr
                word["end"] += start / sr
                if segment.get("speaker"):
                    word["speaker"] = segment["speaker"]
            words.extend(aligned)
    else:
        coarse_segments = [{key: value for key, value in segment.items() if key in {"start", "end", "text", "speaker"}}
                           for segment in segments if segment.get("text", "").strip()]
    state["_diagnostic_phase"] = "output_validation"
    output_segments = word_segments(words) + coarse_segments
    output_segments.sort(key=lambda item: (item["start"], item["end"]))
    metadata = dict(transcript.get("metadata") or {})
    sources = []
    if words:
        sources.append("qwen3_forced_alignment")
    if coarse_segments:
        sources.append("audio_window_bounds_nonlexical")
    metadata.update({"resolved_device": device, "precision": precision,
                     "timestamp_source": ",".join(sources) or "none", "duration_seconds": str(duration),
                     "aligner_revision": ALIGNER_REVISION})
    return {
        "text": text or " ".join(segment.get("text", "").strip() for segment in segments).strip(),
        "language": language,
        "segments": output_segments,
        "word_segments": words,
        "model_used": transcript.get("model_used", ""),
        "metadata": metadata,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--audio", required=True)
    parser.add_argument("--transcript", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--device", choices=("cpu", "cuda", "auto"), default="cpu")
    parser.add_argument("--precision", choices=("float32", "float16", "bfloat16"), default="float32")
    parser.add_argument("--language", default="en")
    args = parser.parse_args()
    output = Path(args.output)
    state = {}
    try:
        transcript = json.loads(Path(args.transcript).read_text(encoding="utf-8"))
        result = align_existing(args.audio, transcript, args.device, args.precision, args.language, temp_parent=output.parent, diagnostic_state=state)
        temporary = output.with_suffix(".partial")
        temporary.write_text(json.dumps(result, ensure_ascii=False, allow_nan=False), encoding="utf-8")
        os.chmod(temporary, 0o600)
        temporary.replace(output)
    except Exception as exc:
        diagnostic = safe_failure(exc, state.get("_diagnostic_phase", "configuration"))
        diagnostic.update(resolved_device=state.get("_resolved_device"),
                          gpu_failure_kind=gpu_failure_kind(exc, state.get("_resolved_device"), state.get("_gpu_execution", False)))
        failure = output.with_name("error.json")
        failure.write_text(json.dumps(diagnostic), encoding="utf-8")
        os.chmod(failure, 0o600)
        print(diagnostic["error"], flush=True)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
