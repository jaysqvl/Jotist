"""Isolated DiariZen and SUPlime inference; model weights are non-commercial."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

# SUPlime uses pyannote.audio. Apply this before either research pipeline loads
# so no recording metadata is exported through Pyannote's default telemetry.
os.environ["PYANNOTE_METRICS_ENABLED"] = "false"

MODELS = {
    "diarizen": {"BUT-FIT/diarizen-wavlm-large-s80-md-v2"},
    "suplime": {"rewayai/suplime", "rewayai/suplime-large"},
}


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


def resolve_device(requested, cuda_available):
    if requested not in {"auto", "cpu", "cuda"}:
        raise ValueError("device must be auto, cpu, or cuda")
    if requested == "cuda" and not cuda_available:
        raise RuntimeError("CUDA was requested but is not available")
    return ("cuda" if cuda_available else "cpu") if requested == "auto" else requested


def serialize_output(output, model, device):
    annotation = getattr(output, "speaker_diarization", output)
    segments = [
        {"start": float(turn.start), "end": float(turn.end), "speaker": str(speaker)}
        for turn, _, speaker in annotation.itertracks(yield_label=True)
    ]
    segments.sort(key=lambda segment: (segment["start"], segment["end"], segment["speaker"]))
    speakers = sorted({segment["speaker"] for segment in segments})
    return {"segments": segments, "speakers": speakers, "speaker_count": len(speakers), "model": model, "resolved_device": device}


def run(args):
    if args.model not in MODELS[args.engine]:
        raise ValueError("unsupported model for selected diarization engine")
    if args.device == "cpu":
        # DiariZen's constructor otherwise chooses CUDA before pipeline.to().
        os.environ["CUDA_VISIBLE_DEVICES"] = ""
    import torch

    device = resolve_device(args.device, torch.cuda.is_available())
    if device == "cpu":
        os.environ["SUPLIME_FP16"] = "0"
    print(f"Using device: {device}", flush=True)
    if args.engine == "diarizen":
        from diarizen.pipelines.inference import DiariZenPipeline
        with gpu_execution(device):
            pipeline = DiariZenPipeline.from_pretrained(args.model)
            pipeline.to(torch.device(device))
        if args.min_speakers is not None:
            pipeline.min_speakers = args.min_speakers
        if args.max_speakers is not None:
            pipeline.max_speakers = args.max_speakers
    else:
        from pyannote.audio import Pipeline
        with gpu_execution(device):
            pipeline = Pipeline.from_pretrained(args.model)
            pipeline.to(torch.device(device))

    with tempfile.TemporaryDirectory(prefix="scriberr-diarization-") as directory:
        waveform_path = Path(directory) / "audio.wav"
        subprocess.run(["ffmpeg", "-nostdin", "-v", "error", "-i", args.audio_file, "-ac", "1", "-ar", "16000", str(waveform_path)], check=True)
        with gpu_execution(device), torch.inference_mode():
            if args.engine == "diarizen":
                output = pipeline(str(waveform_path))
            else:
                # Decode explicitly to avoid torchcodec/FFmpeg ABI coupling.
                import soundfile as sf
                audio, sample_rate = sf.read(waveform_path, dtype="float32", always_2d=True)
                constraints = {key: value for key in ("min_speakers", "max_speakers") if (value := getattr(args, key)) is not None}
                output = pipeline({"waveform": torch.from_numpy(audio.T), "sample_rate": sample_rate}, **constraints)
    Path(args.output).write_text(json.dumps(serialize_output(output, args.model, device), ensure_ascii=False))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("audio_file")
    parser.add_argument("--output", required=True)
    parser.add_argument("--engine", choices=MODELS, required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--device", choices=["auto", "cpu", "cuda"], default="cpu")
    parser.add_argument("--min-speakers", type=int)
    parser.add_argument("--max-speakers", type=int)
    args = parser.parse_args()
    for value in (args.min_speakers, args.max_speakers):
        if value is not None and not 1 <= value <= 20:
            parser.error("speaker constraints must be between 1 and 20")
    if args.min_speakers and args.max_speakers and args.min_speakers > args.max_speakers:
        parser.error("min_speakers cannot exceed max_speakers")
    run(args)


if __name__ == "__main__":
    main()
