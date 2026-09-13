#!/usr/bin/env python3
"""
PyAnnote speaker diarization script.
Processes audio files to identify and separate different speakers.
"""

import argparse
import json
import sys
import os
from copy import deepcopy

# Keep recording metadata local even when the host enables Pyannote telemetry.
# This must run before importing pyannote.audio or a library that imports it.
os.environ["PYANNOTE_METRICS_ENABLED"] = "false"

# Fence explicit CPU jobs before NeMo/PyTorch checkpoint loading can select CUDA.
if "--device=cpu" in sys.argv or any(
    arg == "--device" and next_arg == "cpu"
    for arg, next_arg in zip(sys.argv, sys.argv[1:])
):
    os.environ["CUDA_VISIBLE_DEVICES"] = ""

from pathlib import Path
from pyannote.audio import Pipeline
import torch

# Fix for PyTorch 2.6+ which defaults weights_only=True
# We need to allowlist PyAnnote's custom classes
try:
    from pyannote.audio.core.task import Specifications, Problem, Resolution
    if hasattr(torch.serialization, "add_safe_globals"):
        torch.serialization.add_safe_globals([Specifications, Problem, Resolution])
except ImportError:
    pass
except Exception as e:
    print(f"Warning: Could not add safe globals: {e}")


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


def resolve_device(device):
    if device not in {"auto", "cpu", "cuda"}:
        raise ValueError("device must be auto, cpu, or cuda")
    if device == "cuda" and not torch.cuda.is_available():
        raise RuntimeError("CUDA was requested but is not available")
    return ("cuda" if torch.cuda.is_available() else "cpu") if device == "auto" else device


def apply_segmentation_thresholds(pipeline, onset=None, offset=None):
    """Apply supported probability thresholds without adding pipeline parameters."""
    if onset is None and offset is None:
        return
    params = deepcopy(pipeline.parameters(instantiated=True))
    segmentation = params.get("segmentation", {})
    changed = False
    for label, value, candidate_keys in (
        ("onset", onset, ("onset", "threshold")),
        ("offset", offset, ("offset",)),
    ):
        if value is None:
            continue
        key = next((key for key in candidate_keys if key in segmentation), None)
        if key is None:
            print(f"Skipping segmentation {label}: this pipeline does not expose that probability threshold")
            continue
        if segmentation[key] != value:
            segmentation[key] = value
            changed = True
            print(f"Setting segmentation {key}: {value}")
    if changed:
        # instantiate can mutate a pipeline before failing. Let the caller stop
        # loading on failure; never continue inference with that partial state.
        pipeline.instantiate(params)


def diarize_audio(
    audio_path: str,
    output_file: str,
    hf_token: str = None,
    model: str = "pyannote/speaker-diarization-community-1",
    min_speakers: int = None,
    max_speakers: int = None,
    output_format: str = "rttm",
    device: str = "auto",
    segmentation_onset: float = None,
    segmentation_offset: float = None,
):
    """
    Perform speaker diarization on audio file using PyAnnote.
    """
    print(f"Loading PyAnnote speaker diarization pipeline: {model}")

    try:
        # Initialize the diarization pipeline
        load_options = {"token": hf_token} if hf_token else {}
        pipeline = Pipeline.from_pretrained(model, **load_options)

        resolved_device = resolve_device(device)
        with gpu_execution(resolved_device):
            pipeline.to(torch.device(resolved_device))
        print(f"Using {resolved_device} for diarization")

        # Powerset segmentation (Community-1 and 3.1) has no probability
        # threshold. Its min_duration_off is seconds, not a VAD offset.
        apply_segmentation_thresholds(pipeline, segmentation_onset, segmentation_offset)

        print("Pipeline loaded successfully")
    except Exception as e:
        print(f"Error loading pipeline: {e}")
        print("Make sure you have a valid Hugging Face token and have accepted the model's license")
        sys.exit(1)

    print(f"Processing audio file: {audio_path}")

    try:
        # Run diarization
        diarization_params = {}
        if min_speakers is not None:
            diarization_params["min_speakers"] = min_speakers
        if max_speakers is not None:
            diarization_params["max_speakers"] = max_speakers

        if diarization_params:
            print(f"Using speaker constraints: {diarization_params}")
            with gpu_execution(resolved_device):
                diarization = pipeline(audio_path, **diarization_params)
        else:
            print("Using automatic speaker detection")
            with gpu_execution(resolved_device):
                diarization = pipeline(audio_path)

        print(f"Diarization completed. Saving results to: {output_file}")

        if output_format == "rttm":
            # Save the diarization output to RTTM format
            with open(output_file, "w") as rttm:
                getattr(diarization, "speaker_diarization", diarization).write_rttm(rttm)
        else:
            # Save as JSON format
            save_json_format(diarization, output_file, audio_path, model, resolved_device)

        with open(str(Path(output_file).parent / "runtime.json"), "w") as metadata_file:
            json.dump({"resolved_device": resolved_device}, metadata_file)

        # Print summary
        speakers = set()
        total_speech_time = 0.0

        for turn, _, speaker in iter_speaker_turns(diarization):
            speakers.add(speaker)
            total_speech_time += turn.duration

        print(f"\nDiarization Summary:")
        print(f"  Speakers detected: {len(speakers)}")
        print(f"  Speaker labels: {sorted(speakers)}")
        print(f"  Total speech time: {total_speech_time:.2f} seconds")
        print(f"  Output file saved: {output_file}")

    except Exception as e:
        print(f"Error during diarization: {e}")
        sys.exit(1)


def iter_speaker_turns(diarization):
    """Read labels, rather than track IDs, in both Pyannote output versions."""
    annotation = getattr(diarization, "speaker_diarization", diarization)
    return annotation.itertracks(yield_label=True)


def save_json_format(diarization, output_file: str, audio_path: str, model="pyannote/speaker-diarization-community-1", resolved_device=""):
    """Save diarization results in JSON format."""
    segments = []
    speakers = set()

    for turn, _, speaker in iter_speaker_turns(diarization):
        segments.append({
            "start": turn.start,
            "end": turn.end,
            "speaker": speaker,
            "confidence": 1.0,
            "duration": turn.duration,
        })
        speakers.add(speaker)

    # Sort segments by start time
    segments.sort(key=lambda x: x["start"])

    results = {
        "audio_file": audio_path,
        "model": model,
        "resolved_device": resolved_device,
        "segments": segments,
        "speakers": sorted(speakers),
        "speaker_count": len(speakers),
        "total_duration": max(seg["end"] for seg in segments) if segments else 0,
        "processing_info": {
            "total_segments": len(segments),
            "total_speech_time": sum(seg["duration"] for seg in segments)
        }
    }

    with open(output_file, "w") as f:
        json.dump(results, f, indent=2)


def main():
    parser = argparse.ArgumentParser(
        description="Perform speaker diarization using PyAnnote.audio"
    )
    parser.add_argument(
        "audio_file",
        help="Path to audio file"
    )
    parser.add_argument(
        "--output", "-o",
        required=True,
        help="Output file path"
    )
    parser.add_argument(
        "--hf-token",
        default=os.environ.get("HF_TOKEN"),
        help="Optional Hugging Face access token (otherwise HF_TOKEN or cached login)"
    )
    parser.add_argument(
        "--model",
        default="pyannote/speaker-diarization-community-1",
        help="PyAnnote model to use"
    )
    parser.add_argument(
        "--min-speakers",
        type=int,
        help="Minimum number of speakers"
    )
    parser.add_argument(
        "--max-speakers",
        type=int,
        help="Maximum number of speakers"
    )
    parser.add_argument(
        "--output-format",
        choices=["rttm", "json"],
        default="rttm",
        help="Output format"
    )
    parser.add_argument(
        "--device",
        choices=["cpu", "cuda", "auto"],
        default="auto",
        help="Device to use for computation"
    )
    parser.add_argument(
        "--segmentation-onset",
        type=float,
        help="Voice activity detection onset threshold (0.0-1.0). Lower values detect quieter speech."
    )
    parser.add_argument(
        "--segmentation-offset",
        type=float,
        help="Voice activity detection offset/min_duration_off (0.0-1.0). Lower values are more sensitive to speech endings."
    )

    args = parser.parse_args()

    # Validate input file
    if not os.path.exists(args.audio_file):
        print(f"Error: Audio file not found: {args.audio_file}")
        sys.exit(1)

    # Validate speaker constraints
    if args.min_speakers is not None and args.min_speakers < 1:
        print("Error: min_speakers must be at least 1")
        sys.exit(1)

    if args.max_speakers is not None and args.max_speakers < 1:
        print("Error: max_speakers must be at least 1")
        sys.exit(1)

    if (args.min_speakers is not None and args.max_speakers is not None and
        args.min_speakers > args.max_speakers):
        print("Error: min_speakers cannot be greater than max_speakers")
        sys.exit(1)

    # Create output directory if it doesn't exist
    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        diarize_audio(
            audio_path=args.audio_file,
            output_file=args.output,
            hf_token=args.hf_token,
            model=args.model,
            min_speakers=args.min_speakers,
            max_speakers=args.max_speakers,
            output_format=args.output_format,
            device=args.device,
            segmentation_onset=args.segmentation_onset,
            segmentation_offset=args.segmentation_offset,
        )
    except Exception as e:
        print(f"Error during diarization: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
