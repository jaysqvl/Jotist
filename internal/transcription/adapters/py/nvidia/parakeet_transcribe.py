#!/usr/bin/env python3
"""
NVIDIA Parakeet transcription script with timestamp support.
"""

import argparse
import json
import sys
import os

# Fence explicit CPU jobs before NeMo/PyTorch checkpoint loading can select CUDA.
if "--device=cpu" in sys.argv or any(
    arg == "--device" and next_arg == "cpu"
    for arg, next_arg in zip(sys.argv, sys.argv[1:])
):
    os.environ["CUDA_VISIBLE_DEVICES"] = ""

from pathlib import Path
import nemo.collections.asr as nemo_asr
import torch


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


def transcribe_audio(
    audio_path: str,
    timestamps: bool = True,
    output_file: str = None,
    context_left: int = 256,
    context_right: int = 256,
    include_confidence: bool = True,
    batch_size: int = 1,
    device: str = "auto",
):
    """
    Transcribe audio using NVIDIA Parakeet model.
    """
    # Determine model path
    model_filename = "parakeet-tdt-0.6b-v3.nemo"
    model_path = None

    # Locate project root: derived from VIRTUAL_ENV, which is set by `uv run` to path/.venv
    virtual_env = os.environ.get("VIRTUAL_ENV")
    if not virtual_env:
        print("Error: VIRTUAL_ENV environment variable not set. Script must be run with 'uv run'.")
        sys.exit(1)

    project_root = os.path.dirname(virtual_env)
    model_path = os.path.join(project_root, model_filename)

    if not os.path.exists(model_path):
        print(f"Error during transcription: Can't find {model_filename} in project root: {project_root}")
        sys.exit(1)

    print(f"Loading NVIDIA Parakeet model from: {model_path}")
    if device == "cuda" and not torch.cuda.is_available():
        raise RuntimeError("CUDA was requested but is not available")
    resolved_device = ("cuda" if torch.cuda.is_available() else "cpu") if device == "auto" else device
    asr_model = nemo_asr.models.ASRModel.restore_from(model_path, map_location="cpu")
    with gpu_execution(resolved_device):
        asr_model = asr_model.to(torch.device(resolved_device))
    print(f"Using device: {resolved_device}")

    if resolved_device == "cuda":
        torch.backends.cuda.matmul.allow_tf32 = True
        torch.backends.cudnn.allow_tf32 = True

    # Disable CUDA graphs to fix Error 35 on RTX 2000e Ada GPU
    # Uses change_decoding_strategy() to properly reconfigure the TDT decoder
    from omegaconf import OmegaConf, open_dict

    print("Disabling CUDA graphs in TDT decoder...")
    dec_cfg = asr_model.cfg.decoding

    # Add use_cuda_graph_decoder parameter to greedy config
    with open_dict(dec_cfg.greedy):
        dec_cfg.greedy['use_cuda_graph_decoder'] = False

    # Apply the new decoding strategy (this rebuilds the decoder with our config)
    with gpu_execution(resolved_device):
        asr_model.change_decoding_strategy(dec_cfg)
    print("✓ CUDA graphs disabled successfully")

    asr_model.eval()
    if hasattr(asr_model, "freeze"):
        asr_model.freeze()

    # Configure for long-form audio if context sizes are not default
    if context_left != 256 or context_right != 256:
        print(f"Configuring attention context: left={context_left}, right={context_right}")
        try:
            asr_model.change_attention_model(
                self_attention_model="rel_pos_local_attn",
                att_context_size=[context_left, context_right]
            )
            print("Long-form audio mode enabled")
        except Exception as e:
            print(f"Warning: Failed to configure attention model: {e}")
            print("Continuing with default attention settings")

    print(f"Transcribing: {audio_path}")
    print(f"Batch size: {batch_size}")

    with gpu_execution(resolved_device), torch.inference_mode():
        if timestamps:
            output = asr_model.transcribe([audio_path], timestamps=True, batch_size=batch_size)

            # Extract text and timestamps
            result_data = output[0]
            text = result_data.text
            word_timestamps = result_data.timestamp.get("word", [])
            segment_timestamps = result_data.timestamp.get("segment", [])

            print(f"Transcription: {text}")

            # Prepare output data
            output_data = {
                "transcription": text,
                "language": "en",
                "word_timestamps": word_timestamps,
                "segment_timestamps": segment_timestamps,
                "audio_file": audio_path,
                "model": "parakeet-tdt-0.6b-v3",
                "resolved_device": resolved_device,
                "batch_size": batch_size,
                "context": {
                    "left": context_left,
                    "right": context_right
                }
            }

            if include_confidence:
                # Add confidence scores if available
                if hasattr(result_data, 'confidence') and result_data.confidence:
                    output_data["confidence"] = result_data.confidence

            # Save to file
            if output_file:
                with open(output_file, 'w', encoding='utf-8') as f:
                    json.dump(output_data, f, indent=2, ensure_ascii=False)
                print(f"Results saved to: {output_file}")
            else:
                print(json.dumps(output_data, indent=2, ensure_ascii=False))

        else:
            # Simple transcription without timestamps
            output = asr_model.transcribe([audio_path], batch_size=batch_size)
            text = output[0].text

            output_data = {
                "transcription": text,
                "language": "en",
                "audio_file": audio_path,
                "model": "parakeet-tdt-0.6b-v3",
                "resolved_device": resolved_device,
                "batch_size": batch_size,
            }

            if output_file:
                with open(output_file, 'w', encoding='utf-8') as f:
                    json.dump(output_data, f, indent=2, ensure_ascii=False)
                print(f"Results saved to: {output_file}")
            else:
                print(json.dumps(output_data, indent=2, ensure_ascii=False))


def main():
    parser = argparse.ArgumentParser(
        description="Transcribe audio using NVIDIA Parakeet model"
    )
    parser.add_argument("audio_file", help="Path to audio file")
    parser.add_argument(
        "--timestamps", action="store_true", default=True,
        help="Include word and segment level timestamps"
    )
    parser.add_argument(
        "--no-timestamps", dest="timestamps", action="store_false",
        help="Disable timestamps"
    )
    parser.add_argument(
        "--output", "-o", help="Output file path"
    )
    parser.add_argument(
        "--context-left", type=int, default=256,
        help="Left attention context size (default: 256)"
    )
    parser.add_argument(
        "--context-right", type=int, default=256,
        help="Right attention context size (default: 256)"
    )
    parser.add_argument(
        "--include-confidence", action="store_true", default=True,
        help="Include confidence scores"
    )
    parser.add_argument(
        "--no-confidence", dest="include_confidence", action="store_false",
        help="Exclude confidence scores"
    )
    parser.add_argument(
        "--batch-size", type=int, default=1,
        help="Batch size for NeMo transcription (default: 1)"
    )

    parser.add_argument("--device", choices=["auto", "cpu", "cuda"], default="auto")
    args = parser.parse_args()

    # Validate input file
    if not os.path.exists(args.audio_file):
        print(f"Error: Audio file not found: {args.audio_file}")
        sys.exit(1)

    try:
        transcribe_audio(
            audio_path=args.audio_file,
        device=args.device,
            timestamps=args.timestamps,
            output_file=args.output,
            context_left=args.context_left,
            context_right=args.context_right,
            include_confidence=args.include_confidence,
            batch_size=args.batch_size,
        )
    except Exception as e:
        print(f"Error during transcription: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
