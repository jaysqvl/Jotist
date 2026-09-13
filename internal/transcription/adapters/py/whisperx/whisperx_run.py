"""Run the pinned WhisperX CLI with explicit device selection and metadata."""
import json
import os
from pathlib import Path
import sys

# WhisperX imports Pyannote internally; disable its recording-metadata telemetry
# before loading any ML libraries, including when this runner is used directly.
os.environ["PYANNOTE_METRICS_ENABLED"] = "false"


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


def main():
    device_index = sys.argv.index("--device") + 1
    if sys.argv[device_index] == "cpu":
        os.environ["CUDA_VISIBLE_DEVICES"] = ""
    import torch
    from whisperx.__main__ import cli

    requested = sys.argv[device_index]
    device = resolve_device(requested, torch.cuda.is_available())
    if requested == "auto" and device == "cpu" and "--compute_type" in sys.argv:
        precision_index = sys.argv.index("--compute_type") + 1
        if sys.argv[precision_index] in {"float16", "bfloat16", "int8_float16"}:
            sys.argv[precision_index] = "float32"
    sys.argv[device_index] = device
    output_directory = Path(sys.argv[sys.argv.index("--output_dir") + 1])
    print(f"Using device: {device}", flush=True)
    with gpu_execution(device):
        cli()
    for path in output_directory.glob("*.json"):
        data = json.loads(path.read_text())
        if "segments" in data:
            data["resolved_device"] = device
            path.write_text(json.dumps(data, ensure_ascii=False))


if __name__ == "__main__":
    main()
