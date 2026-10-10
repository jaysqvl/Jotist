"""Shared speech preparation. Samples and coordinates remain on the source timeline.

Adapters opt in through a pinned contract. Detection runs on their resolved
device before ASR loads; alignment and diarization still use the full waveform.
"""
from __future__ import annotations

import gc
import hashlib
import io
from importlib import metadata, resources
import json
from pathlib import Path
import time

from backends import RecognitionError


class AudioPreparationError(RecognitionError):
    """Required speech detection failed; never continue as unfiltered recognition."""


def default_contract():
    return json.loads(Path(__file__).with_name("speech_detection.json").read_text(encoding="utf-8"))


def required_contract(config):
    expected = default_contract() if config.get("engine") == "cohere" else None
    # Missing means an older/direct worker caller. Null/changed contracts cannot
    # silently disable an adapter's mandatory preparation.
    if "speech_detection" in config and config["speech_detection"] != expected:
        raise AudioPreparationError("Required speech detection configuration is incompatible.")
    return expected


def padded_ranges(intervals, samples, sample_rate, contract):
    """Pad and merge detected intervals without concatenating or modifying audio."""
    padding = sample_rate * contract["context_padding_ms"] // 1000
    gap = sample_rate * contract["merge_gap_ms"] // 1000
    ranges = []
    previous_end = 0
    for interval in intervals:
        start, end = interval.get("start"), interval.get("end")
        if (type(start) is not int or type(end) is not int
                or not 0 <= previous_end <= start < end <= samples):
            raise AudioPreparationError("Speech detection returned invalid audio intervals.")
        previous_end = end
        start, end = max(0, start - padding), min(samples, end + padding)
        if ranges and start <= ranges[-1][1] + gap:
            ranges[-1] = (ranges[-1][0], max(ranges[-1][1], end))
        else:
            ranges.append((start, end))
    return ranges


class _DeviceDetector:
    """Move one classifier frame at a time, keeping long recordings off VRAM."""
    def __init__(self, model, device):
        self.model, self.device = model, device

    def reset_states(self):
        self.model.reset_states()

    def __call__(self, frame, sample_rate):
        return self.model(frame.to(device=self.device), sample_rate)


def detect_speech(audio, sample_rate, device, contract, progress=None):
    """Use the package's bundled, checksum-verified JIT model; no hub download."""
    started = time.monotonic()
    model = waveform = detector = None
    torch = None
    try:
        if contract != default_contract() or sample_rate != contract["sample_rate"]:
            raise AudioPreparationError("Required speech detection configuration is incompatible.")
        if metadata.version("silero-vad") != contract["package_version"]:
            raise AudioPreparationError("Required speech detection runtime does not match its pinned version.")
        import torch
        from silero_vad.utils_vad import get_speech_timestamps
        asset = resources.files("silero_vad.data").joinpath("silero_vad.jit")
        model_bytes = asset.read_bytes()
        if hashlib.sha256(model_bytes).hexdigest() != contract["model_sha256"]:
            raise AudioPreparationError("Required speech detection model failed its integrity check.")
        model = torch.jit.load(io.BytesIO(model_bytes), map_location=device).eval()
        if any(parameter.device.type != device for parameter in model.parameters()):
            raise AudioPreparationError("Speech detection did not use the requested device.")
        # The public detector consumes 512-sample frames. Keep its full input
        # on the host and transfer only each frame; an hours-long recording
        # must not require hours of audio resident on the selected GPU.
        waveform = torch.from_numpy(audio)
        detector = _DeviceDetector(model, device)
        with torch.inference_mode():
            intervals = get_speech_timestamps(
                waveform, detector, sampling_rate=sample_rate,
                threshold=contract["threshold"], neg_threshold=contract["negative_threshold"],
                min_speech_duration_ms=contract["min_speech_duration_ms"],
                min_silence_duration_ms=contract["min_silence_duration_ms"],
                max_speech_duration_s=float("inf"), speech_pad_ms=0,
                return_seconds=False, progress_tracking_callback=progress,
            )
        ranges = padded_ranges(intervals, len(audio), sample_rate, contract)
        return ranges, {
            "speech_detection": contract["implementation"],
            "speech_detection_device": device,
            "speech_detection_model_sha256": contract["model_sha256"],
            "speech_detection_range_count": str(len(ranges)),
            "speech_detection_audio_seconds": str(sum(end - start for start, end in ranges) / sample_rate),
            "speech_detection_elapsed_seconds": str(time.monotonic() - started),
        }
    except AudioPreparationError:
        raise
    except Exception as exc:
        raise AudioPreparationError("Required speech detection could not prepare the recording.") from exc
    finally:
        # No detector weights or full-recording CUDA tensor stay resident while
        # the recognizer loads. Return only integer coordinates and safe metrics.
        del model, waveform, detector
        gc.collect()
        if torch is not None and device == "cuda":
            torch.cuda.empty_cache()
