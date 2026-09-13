#!/usr/bin/env python3
"""Durable Canary boundaries pinned to NeMo v3.0.0.

Recognition retains NeMo's timestamp prompt and native chunking. Only its CTC
call is deferred; the original chunk hypotheses and merge inputs are saved.
Alignment restores the embedded CTC artifact and main tokenizer, never the
recognizer. No pickle, model weights, audio samples, or credentials enter the
recognition JSON. Source sample hashes prove reconstructed cuts are identical.
"""
import argparse
import contextlib
import errno
import hashlib
import json
import math
import os
import resource
import sys
import time
from pathlib import Path
import shutil
import tarfile
import tempfile
from types import SimpleNamespace

if "--device" in os.sys.argv and os.sys.argv[os.sys.argv.index("--device") + 1] == "cpu":
    os.environ["CUDA_VISIBLE_DEVICES"] = ""

import numpy as np
import soundfile as sf
import torch
from omegaconf import OmegaConf, open_dict
# Apply the guarded compatibility import by absolute path, including under -I.
import runpy as _nvidia_runpy
from pathlib import Path as _NvidiaPath
_nvidia_runpy.run_path(str(_NvidiaPath(__file__).resolve().with_name("nvidia_compat.py")))

import nemo.collections.asr as nemo_asr
from nemo.collections.asr.models import aed_multitask_models as aed
from nemo.collections.asr.data import audio_to_text_lhotse_prompted
from nemo.collections.asr.parts.utils import aligner_utils, chunking_utils, timestamp_utils
from nemo.collections.asr.parts.utils.rnnt_utils import Hypothesis
from nemo.collections.asr.parts.mixins.mixins import ASRBPEMixin
from nemo.core.classes.common import Serialization
from nemo.core.connectors.save_restore_connector import SaveRestoreConnector
import canary_transcribe as baseline

SCHEMA = "canary-native-recognition-v1"
MODEL_SHA256 = "ae5ef1bf06812a95a1594a8f5f0ee9c51f35418e5ba96939fa6b98ab00431094"
CTC_SHA256 = "2155f5642f9d27d73bd0c41693ddbb2420a95b32acc11576c9b36638f7ece795"
SOURCE_HASHES = {
    "aed_multitask_models.py": "003ca55ba61146cec555e463f7bbabe10eea0396c3c307f3e3f7f6465fed24e3",
    "chunking_utils.py": "43eab4c597ce15da7801098580b3b1c21edb9dd5518e57ca312d4bc1d1bd7013",
    "timestamp_utils.py": "74015fdbba8c0af440c3ffcdc3f9dd06046a08177f1e2a09ceea2b70112a4531",
    "aligner_utils.py": "d5d0a70490a1df9b9f62146bb1b7571f3a886a17c57c0a9f560a722eb09a358b",
    "audio_to_text_lhotse_prompted.py": "af5f4cab7eab6a86c8feb93a738bf69e896e75d90817f8feff122295afb912dc",
}


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as source:
        for block in iter(lambda: source.read(8 * 1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def verify_runtime():
    for module in (aed, chunking_utils, timestamp_utils, aligner_utils, audio_to_text_lhotse_prompted):
        path = Path(module.__file__)
        if sha256_file(path) != SOURCE_HASHES[path.name]:
            raise ValueError("Canary staged runtime differs from pinned NeMo v3.0.0; refresh the pinned environment")


def extract_assets(model_path, cache_root):
    """Extract the exact auxiliary archive members, with atomic cache publication."""
    model_hash = sha256_file(model_path)
    root = Path(cache_root)
    root.mkdir(parents=True, exist_ok=True)
    target = root / model_hash
    if (target / "complete.json").is_file():
        manifest = json.loads((target / "complete.json").read_text(encoding="utf-8"))
        if manifest.get("model_sha256") != model_hash or not manifest.get("files"):
            raise ValueError("Invalid extracted Canary artifact manifest")
        for name, checksum in manifest["files"].items():
            if Path(name).name != name or sha256_file(target / name) != checksum:
                raise ValueError("Extracted Canary artifact integrity check failed")
        return target, model_hash
    temporary = Path(tempfile.mkdtemp(prefix=".extract-", dir=root))
    try:
        with tarfile.open(model_path, "r:*") as archive:
            for member in archive:
                name = member.name.removeprefix("./")
                if not member.isfile() or name == "model_weights.ckpt":
                    continue
                if Path(name).name != name:
                    raise ValueError("Unexpected nested Canary model artifact")
                source = archive.extractfile(member)
                with open(temporary / name, "wb") as destination:
                    shutil.copyfileobj(source, destination)
        for required in ("model_config.yaml", "timestamps_asr_model_config.yaml", "timestamps_asr_model_weights.ckpt"):
            if not (temporary / required).is_file():
                raise ValueError("Canary archive is missing its embedded timestamp artifact")
        manifest = {"model_sha256": model_hash, "files": {p.name: sha256_file(p) for p in temporary.iterdir() if p.is_file()}}
        (temporary / "complete.json").write_text(json.dumps(manifest), encoding="utf-8")
        try:
            temporary.rename(target)
        except OSError as error:
            if error.errno not in (errno.EEXIST, errno.ENOTEMPTY) or not (target / "complete.json").is_file():
                raise
            # Another process won publication. Validate its complete artifact
            # rather than accepting the directory name as an integrity check.
            return extract_assets(model_path, cache_root)
        return target, model_hash
    finally:
        if temporary.exists():
            shutil.rmtree(temporary)


def json_value(value):
    if isinstance(value, torch.Tensor):
        return value.detach().cpu().tolist()
    if isinstance(value, np.ndarray):
        return value.tolist()
    if isinstance(value, (list, tuple)):
        return [json_value(v) for v in value]
    if isinstance(value, dict):
        return {key: json_value(v) for key, v in value.items()}
    return value


def serialize_hypothesis(hypothesis):
    return {name: json_value(getattr(hypothesis, name, None)) for name in
            ("text", "score", "y_sequence", "frame_confidence", "token_confidence", "word_confidence", "confidence")}


def restore_hypothesis(saved):
    hyp = Hypothesis(score=saved.get("score") or 0.0, y_sequence=torch.tensor(saved["y_sequence"], dtype=torch.long), text=saved["text"])
    for name in ("frame_confidence", "token_confidence", "word_confidence", "confidence"):
        if saved.get(name) is not None:
            setattr(hyp, name, saved[name])
    return hyp


class DeferredTimestampModel:
    """Preserves NeMo's Canary-v2/native-chunk branch without model residency."""
    def to(self, *_args, **_kwargs):
        return self


@contextlib.contextmanager
def capture_native_boundary(model, captured):
    original_output = model._transcribe_output_processing
    original_decode = model.decoding.decode_predictions_tensor
    original_align = aed.get_forced_aligned_timestamps_with_external_model
    original_merge = aed.merge_parallel_chunks
    current = {}

    def decode(*args, **kwargs):
        hypotheses = original_decode(*args, **kwargs)
        current["hypotheses"] = [serialize_hypothesis(h) for h in hypotheses]
        return hypotheses

    def defer_alignment(**kwargs):
        current["timestamp_type"] = kwargs["timestamp_type"]
        current["native_alignment_batch_size"] = kwargs["batch_size"]
        for hypothesis in kwargs["main_model_predictions"]:
            hypothesis.timestamp = {"word": [], "segment": [], "char": []}
        return kwargs["main_model_predictions"]

    def preview_merge(**kwargs):
        # This is a partial-text preview only. Final text/timestamps are rebuilt
        # by the original timestamps=True merge after the CTC checkpoint.
        kwargs["timestamps"] = False
        result = original_merge(**kwargs)
        result.timestamp = {"word": [], "segment": []}
        return result

    def output(outputs, config):
        current.clear()
        batch = outputs["batch"]
        if not isinstance(batch, aed.PromptedAudioToTextMiniBatch) or len(batch.cuts) != 1:
            raise ValueError("Canary staged input requires one source cut per native batch")
        lengths = batch.audio_lens.detach().cpu().tolist()
        chunk_stride = batch.audio.shape[1] - 16000 if len(lengths) > 1 else 0
        current.update({"cut_id": batch.cuts[0].id, "cut_start_sample": round(batch.cuts[0].start * 16000),
                        "encoded_lengths": outputs["encoded_lengths"].detach().cpu().tolist(),
                        "audio": [{"start_sample": round(batch.cuts[0].start * 16000) + i * chunk_stride,
                                   "length_samples": length,
                                   "sha256": hashlib.sha256(batch.audio[i, :length].detach().cpu().numpy().astype("<f4").tobytes()).hexdigest()}
                                  for i, length in enumerate(lengths)],
                        "native_alignment_batch_size": len(lengths), "timestamp_type": "char" if len(lengths) > 1 else ["word", "segment"]})
        result = original_output(outputs, config)
        captured.append(json_value(dict(current)))
        return result

    model._transcribe_output_processing = output
    model.decoding.decode_predictions_tensor = decode
    aed.get_forced_aligned_timestamps_with_external_model = defer_alignment
    aed.merge_parallel_chunks = preview_merge
    try:
        yield
    finally:
        model._transcribe_output_processing = original_output
        model.decoding.decode_predictions_tensor = original_decode
        aed.get_forced_aligned_timestamps_with_external_model = original_align
        aed.merge_parallel_chunks = original_merge


class TokenizerOnly(ASRBPEMixin):
    from_config_dict = staticmethod(Serialization.from_config_dict)

    def __init__(self, config, assets):
        self.cfg = OmegaConf.create({"tokenizer": OmegaConf.to_container(config.tokenizer)})
        self.assets = assets
        self.artifacts = {}
        self._setup_tokenizer(self.cfg.tokenizer)

    def register_artifact(self, _key, source, verify_src_exists=True):
        if not source:
            return source
        path = self.assets / Path(source.removeprefix("nemo:")).name
        if verify_src_exists and not path.is_file():
            raise ValueError("Missing exact Canary tokenizer artifact")
        return str(path)


def restore_aligner(assets, model_path, device):
    connector = SaveRestoreConnector()
    connector.model_config_yaml = "timestamps_asr_model_config.yaml"
    connector.model_weights_ckpt = "timestamps_asr_model_weights.ckpt"
    connector.model_extracted_dir = str(assets)
    with baseline.gpu_execution("cpu"):
        model = nemo_asr.models.ASRModel.restore_from(str(model_path), save_restore_connector=connector, map_location=torch.device("cpu"))
    with baseline.gpu_execution(device.type):
        return model.float().to(device).eval()


def normalized_result(results, offsets, args):
    text, words, segments = [], [], []
    for result, offset in zip(results, offsets):
        value, word, segment, _ = baseline.collect_result(result, offset, args.timestamps, args.include_confidence)
        if value.strip():
            text.append(value.strip())
        words.extend({"word": w["word"], "start": w["start"], "end": w["end"], "score": 1.0} for w in word)
        segments.extend({"text": s["segment"], "start": s["start"], "end": s["end"]} for s in segment)
    return {"text": " ".join(text), "language": args.target_lang if args.task == "translate" else args.source_lang,
            "segments": segments, "word_segments": words, "model_used": "canary-1b-v2", "metadata": {}}


def source_calls(args, temporary):
    if args.chunking:
        return baseline.split_audio_file(args.audio, args.chunk_len, temporary)
    return [{"path": args.audio, "start": 0.0}]


def ctc_window_layout(sample_count, samples_per_frame, window_seconds, overlap_seconds):
    """Exact frame ownership: windows include context; centers tile once."""
    if window_seconds not in (10, 20) or overlap_seconds < 2 or window_seconds <= 2 * overlap_seconds:
        raise ValueError("Unqualified Canary CTC window or context bound")
    window_frames = int(round(window_seconds * 16000 / samples_per_frame))
    context_frames = int(math.ceil(overlap_seconds * 16000 / samples_per_frame))
    core_frames = window_frames - 2 * context_frames
    if core_frames < 1:
        raise ValueError("Canary CTC window has no retained center")
    total_frames = math.ceil(sample_count / samples_per_frame)
    result = []
    for core_start in range(0, total_frames, core_frames):
        core_end = min(total_frames, core_start + core_frames)
        start, end = max(0, core_start - context_frames), min(total_frames, core_end + context_frames)
        result.append((start, end, core_start - start, core_end - start))
    return result


def windowed_ctc_hypotheses(model, audio, predictions, window_seconds, overlap_seconds):
    """Bound CTC acoustics only; original text and full-cut Viterbi are retained."""
    step = model.cfg.preprocessor.window_stride * model.encoder.subsampling_factor * 16000
    samples_per_frame = int(round(step))
    if abs(step - samples_per_frame) > 1e-6 or samples_per_frame != 1280:
        raise ValueError("Canary CTC frame grid differs from the qualified 80ms runtime")
    results = []
    for waveform, prediction in zip(audio, predictions):
        pieces = []
        for start, end, keep_start, keep_end in ctc_window_layout(len(waveform), samples_per_frame, window_seconds, overlap_seconds):
            chunk = waveform[start * samples_per_frame:min(len(waveform), end * samples_per_frame)]
            hypothesis = model.transcribe([chunk], return_hypotheses=True, batch_size=1)[0]
            logits = hypothesis.y_sequence.detach().cpu()
            if logits.ndim != 2 or logits.shape[0] != end - start:
                raise ValueError("Canary CTC window frame cardinality changed; refusing to pad or drop frames")
            pieces.append(logits[keep_start:keep_end].clone())
            del logits, hypothesis
        joined = torch.cat(pieces, dim=0)
        if joined.shape[0] != math.ceil(len(waveform) / samples_per_frame):
            raise ValueError("Canary CTC stitched frame ownership is incomplete")
        results.append(Hypothesis(score=0.0, y_sequence=joined, text=prediction.text))
    return results


def restore_audio_cut(path, cut):
    samples, rate = sf.read(path, start=cut["start_sample"], frames=cut["length_samples"], dtype="float32")
    if rate != 16000 or samples.ndim != 1:
        raise ValueError("Canary alignment requires the original prepared mono 16kHz audio")
    # Pinned NeMo's Lhotse loader pads subsecond source cuts to one second,
    # equally on both sides. Reconstruct that preprocessing, then verify bytes.
    if cut["start_sample"] == 0 and cut["length_samples"] == 16000 and len(samples) < 16000:
        missing = 16000 - len(samples)
        samples = np.pad(samples, (missing // 2, missing - missing // 2))
    if len(samples) != cut["length_samples"] or hashlib.sha256(samples.astype("<f4").tobytes()).hexdigest() != cut["sha256"]:
        raise ValueError("Canary alignment source samples differ from recognition")
    return samples


def recognize(args, assets, model_hash, device):
    with baseline.gpu_execution("cpu"):
        config = nemo_asr.models.ASRModel.restore_from(args.model, return_config=True)
        with open_dict(config):
            config.restore_timestamps_model = False
        model = nemo_asr.models.ASRModel.restore_from(args.model, override_config_path=config, map_location=torch.device("cpu"))
    object.__setattr__(model, "timestamps_asr_model", DeferredTimestampModel())
    with baseline.gpu_execution(device.type):
        model = baseline.configure_model(model, device, args.precision)
    calls, results, offsets = [], [], []
    with tempfile.TemporaryDirectory(prefix="canary-stage-", dir=Path(args.output).parent) as temporary:
        for call in source_calls(args, temporary):
            batches = []
            with capture_native_boundary(model, batches), torch.inference_mode(), baseline.gpu_execution(device.type):
                result = baseline.transcribe_one(model, call["path"], args.source_lang, args.target_lang, args.timestamps, args.batch_size)
            calls.append({"offset_seconds": call["start"], "batches": batches})
            results.append(result)
            offsets.append(call["start"])
    output = normalized_result(results, offsets, args)
    output["recognition_state"] = {"schema": SCHEMA, "model_sha256": model_hash, "source_sha256": sha256_file(args.audio),
                                   "sample_rate": 16000, "subsampling_factor": model.encoder.subsampling_factor,
                                   "window_stride": model.cfg.preprocessor.window_stride, "calls": calls,
                                   "explicit_chunking": args.chunking, "chunk_duration": args.chunk_len,
                                   "timestamps_requested": args.timestamps,
                                   "native_alignment_batch_size": max((b["native_alignment_batch_size"] for c in calls for b in c["batches"]), default=1)}
    output["metadata"].update({"recognition_stage": "complete", "alignment_stage": "pending" if args.timestamps else "not_requested",
                               "text_status": "partial_native_merge" if args.timestamps else "complete",
                               "resolved_device": device.type, "resolved_precision": args.precision})
    return output


def align(args, assets, model_hash, device):
    upstream = json.loads(Path(args.upstream).read_text(encoding="utf-8"))
    state = upstream.get("recognition_state", {})
    if state.get("schema") != SCHEMA or state.get("model_sha256") != model_hash or state.get("source_sha256") != sha256_file(args.audio):
        raise ValueError("Canary recognition checkpoint does not match the source/model contract")
    if args.precision != "float32":
        raise ValueError("The qualified native Canary CTC alignment requires float32")
    model = restore_aligner(assets, args.model, device)
    tokenizer = TokenizerOnly(OmegaConf.load(assets / "model_config.yaml"), assets).tokenizer
    decoding = SimpleNamespace(decode_tokens_to_str=tokenizer.tokens_to_text)
    results, offsets, used_batches = [], [], []
    args.chunking, args.chunk_len = state["explicit_chunking"], state["chunk_duration"]
    with tempfile.TemporaryDirectory(prefix="canary-alignment-", dir=Path(args.output).parent) as temporary:
        sources = source_calls(args, temporary)
        if len(sources) != len(state["calls"]):
            raise ValueError("Canary source cut layout changed")
        for source, call in zip(sources, state["calls"]):
            merged = []
            for batch in call["batches"]:
                audio = []
                for cut in batch["audio"]:
                    audio.append(torch.from_numpy(restore_audio_cut(source["path"], cut)))
                hypotheses = [restore_hypothesis(h) for h in batch["hypotheses"]]
                batch_size = min(args.alignment_batch_size or batch["native_alignment_batch_size"], len(audio))
                used_batches.append(batch_size)
                with torch.inference_mode(), baseline.gpu_execution(device.type):
                    alignment_audio = audio
                    if args.alignment_window_seconds:
                        alignment_audio = windowed_ctc_hypotheses(model, audio, hypotheses, args.alignment_window_seconds, args.overlap_seconds)
                    hypotheses = timestamp_utils.get_forced_aligned_timestamps_with_external_model(
                        audio=alignment_audio, external_ctc_model=model, main_model_predictions=hypotheses, batch_size=batch_size,
                        timestamp_type=batch["timestamp_type"], viterbi_device=device, has_hypotheses=bool(args.alignment_window_seconds))
                    if len(hypotheses) > 1:
                        result = chunking_utils.merge_parallel_chunks(hypotheses, torch.tensor(batch["encoded_lengths"]), None, True,
                                                                      state["subsampling_factor"], state["window_stride"], decoding)
                    else:
                        result = hypotheses[0]
                result.id = batch["cut_id"]
                merged.append(result)
            merged = chunking_utils.merge_all_hypotheses(merged, True, state["subsampling_factor"])
            if len(merged) != 1:
                raise ValueError("Unexpected Canary merged source cardinality")
            results.append(merged[0])
            offsets.append(call["offset_seconds"])
    output = normalized_result(results, offsets, args)
    output["metadata"].update({"recognition_stage": "reused", "alignment_stage": "complete", "resolved_device": device.type,
                               "resolved_precision": "float32", "alignment_batch_size": str(max(used_batches, default=1)),
                               "recognizer_loaded_for_alignment": "false", "alignment_artifact_sha256": sha256_file(assets / "timestamps_asr_model_weights.ckpt")})
    if args.alignment_window_seconds:
        output["metadata"].update({"alignment_window_seconds": str(args.alignment_window_seconds),
                                   "alignment_context_each_side_seconds": str(args.overlap_seconds),
                                   "alignment_stitching_version": "canary-ctc-center-logits-v1",
                                   "alignment_window_scope": "ctc_encoder_input_including_context",
                                   "alignment_viterbi_scope": "full_native_recognition_cut"})
    return output


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("stage", choices=["recognition", "alignment"])
    for name in ("audio", "output", "model", "assets"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--upstream")
    parser.add_argument("--device", choices=["cpu", "cuda"], required=True)
    parser.add_argument("--precision", choices=["float16", "bfloat16", "float32"], default="float32")
    parser.add_argument("--source-lang", default="en")
    parser.add_argument("--target-lang", default="en")
    parser.add_argument("--task", choices=["transcribe", "translate"], default="transcribe")
    parser.add_argument("--batch-size", type=int, default=1)
    parser.add_argument("--alignment-batch-size", type=int, default=0)
    parser.add_argument("--alignment-window-seconds", type=int, default=0)
    parser.add_argument("--overlap-seconds", type=float, default=2)
    parser.add_argument("--chunk-len", type=int, default=40)
    parser.add_argument("--chunking", action="store_true")
    parser.add_argument("--timestamps", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--include-confidence", action=argparse.BooleanOptionalAction, default=True)
    args = parser.parse_args()
    started = time.monotonic()
    try:
        verify_runtime()
        if args.batch_size < 1 or args.alignment_batch_size < 0:
            raise ValueError("Invalid Canary batch size")
        if args.alignment_window_seconds:
            ctc_window_layout(16000, 1280, args.alignment_window_seconds, args.overlap_seconds)
        device = baseline.resolve_device(args.device)
        if device.type == "cpu" and args.precision != "float32":
            raise ValueError("Canary CPU stage requires explicitly selected float32")
        baseline.configure_torch()
        assets, model_hash = extract_assets(args.model, args.assets)
        if model_hash != MODEL_SHA256 or sha256_file(assets / "timestamps_asr_model_weights.ckpt") != CTC_SHA256:
            raise ValueError("Canary stage artifacts differ from the pinned checkpoint")
        result = recognize(args, assets, model_hash, device) if args.stage == "recognition" else align(args, assets, model_hash, device)
        destination = Path(args.output)
        temporary = destination.with_suffix(".tmp")
        temporary.write_text(json.dumps(result, ensure_ascii=False, allow_nan=False), encoding="utf-8")
        temporary.replace(destination)
    except Exception:
        # Execution scopes emit the structured GPU marker before propagation.
        # Model access, source integrity, and contract failures never emit it.
        raise
    finally:
        metrics = {"scope": "stage_worker_self", "process_peak_rss_bytes": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss * (1 if sys.platform == "darwin" else 1024),
                   "elapsed_seconds": time.monotonic() - started, "device": args.device, "precision": args.precision}
        try:
            if args.device == "cuda" and torch.cuda.is_initialized():
                metrics["torch_peak_allocated_bytes"] = torch.cuda.max_memory_allocated()
                metrics["torch_peak_reserved_bytes"] = torch.cuda.max_memory_reserved()
        except RuntimeError:
            # Telemetry must not replace an original CUDA execution error.
            metrics["torch_metrics_unavailable"] = True
        print("SCRIBERR_STAGE_METRICS=" + json.dumps(metrics), flush=True)


if __name__ == "__main__":
    main()
