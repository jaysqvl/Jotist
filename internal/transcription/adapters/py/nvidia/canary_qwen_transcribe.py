#!/usr/bin/env python3
"""
NVIDIA Canary-Qwen transcription script.
"""

import argparse
import json
import math
import os
import sys

# Fence explicit CPU jobs before NeMo/PyTorch checkpoint loading can select CUDA.
if "--device=cpu" in sys.argv or any(
    arg == "--device" and next_arg == "cpu"
    for arg, next_arg in zip(sys.argv, sys.argv[1:])
):
    os.environ["CUDA_VISIBLE_DEVICES"] = ""

import tempfile
from typing import Iterable, List

import librosa
import soundfile as sf
import torch
# Apply the guarded compatibility import by absolute path, including under -I.
import runpy as _nvidia_runpy
from pathlib import Path as _NvidiaPath
_nvidia_runpy.run_path(str(_NvidiaPath(__file__).resolve().with_name("nvidia_compat.py")))

from nemo.collections.speechlm2.models import SALM


AUTO_MIN_NEW_TOKENS = 512
AUTO_MAX_NEW_TOKENS = 2048
AUTO_TOKENS_PER_SECOND = 12
AUTO_TOKEN_HEADROOM = 256
AUTO_MAX_SPLIT_DEPTH = 2


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


def batched(items: List[dict], batch_size: int) -> Iterable[List[dict]]:
    for idx in range(0, len(items), batch_size):
        yield items[idx:idx + batch_size]


def split_audio_file(audio_path: str, chunk_duration_secs: float, temp_dir: str) -> List[dict]:
    audio, sr = librosa.load(audio_path, sr=16000, mono=True)
    chunk_samples = int(chunk_duration_secs * sr)
    if chunk_samples <= 0:
        raise ValueError("chunk_duration_secs must be greater than 0")

    chunks = []
    for chunk_index, start_sample in enumerate(range(0, len(audio), chunk_samples)):
        end_sample = min(start_sample + chunk_samples, len(audio))
        chunk_audio = audio[start_sample:end_sample]
        start_time = start_sample / sr
        end_time = end_sample / sr
        chunk_path = os.path.join(temp_dir, f"canary_qwen_chunk_{chunk_index}.wav")
        sf.write(chunk_path, chunk_audio, sr)
        chunks.append({
            "path": chunk_path,
            "start": start_time,
            "end": end_time,
        })

    return chunks


def resolve_device(device: str) -> torch.device:
    if device == "auto":
        return torch.device("cuda" if torch.cuda.is_available() else "cpu")
    if device == "cuda" and not torch.cuda.is_available():
        raise RuntimeError("CUDA was requested but is not available")
    return torch.device(device)


def configure_model(model, device: torch.device, precision: str):
    if device.type == "cuda":
        torch.backends.cuda.matmul.allow_tf32 = True
        torch.backends.cudnn.allow_tf32 = True

    if precision == "bfloat16":
        model = model.bfloat16()
    elif precision == "float16":
        if device.type == "cuda":
            model = model.half()
        else:
            print("float16 on CPU is not supported well; using float32 instead")
            model = model.float()
    else:
        model = model.float()

    return model.eval().to(device)


def decode_answer(model, answer_ids) -> str:
    if hasattr(answer_ids, "cpu"):
        answer_ids = answer_ids.cpu()
    if hasattr(answer_ids, "tolist"):
        answer_ids = answer_ids.tolist()
    text = model.tokenizer.ids_to_text(answer_ids)
    return text.replace("<|endoftext|>", "").strip()


def token_ids(answer_ids) -> List[int]:
    if hasattr(answer_ids, "detach"):
        answer_ids = answer_ids.detach()
    if hasattr(answer_ids, "cpu"):
        answer_ids = answer_ids.cpu()
    if hasattr(answer_ids, "tolist"):
        answer_ids = answer_ids.tolist()
    while isinstance(answer_ids, list) and len(answer_ids) == 1 and isinstance(answer_ids[0], list):
        answer_ids = answer_ids[0]
    return [int(token) for token in answer_ids]


def generation_completed(answer_ids, eos_token_id: int) -> bool:
    return eos_token_id in token_ids(answer_ids)


def auto_token_budget(duration_seconds: float) -> int:
    proposed = math.ceil(max(0.0, duration_seconds) * AUTO_TOKENS_PER_SECOND) + AUTO_TOKEN_HEADROOM
    return min(AUTO_MAX_NEW_TOKENS, max(AUTO_MIN_NEW_TOKENS, proposed))


def generation_prompt(prompt_text: str, audio_path: str) -> List[List[dict]]:
    return [[{
        "role": "user",
        "content": prompt_text,
        "audio": [audio_path],
    }]]


def split_chunk(chunk: dict, temp_dir: str) -> List[dict]:
    audio, sample_rate = librosa.load(chunk["path"], sr=16000, mono=True)
    midpoint = len(audio) // 2
    if midpoint <= 0 or midpoint >= len(audio):
        return []
    duration = chunk["end"] - chunk["start"]
    pieces = []
    for index, samples in enumerate((audio[:midpoint], audio[midpoint:])):
        descriptor, path = tempfile.mkstemp(prefix="canary_qwen_split_", suffix=f"_{index}.wav", dir=temp_dir)
        os.close(descriptor)
        sf.write(path, samples, sample_rate)
        start = chunk["start"] if index == 0 else chunk["start"] + duration * (midpoint / len(audio))
        end = chunk["start"] + duration * (midpoint / len(audio)) if index == 0 else chunk["end"]
        pieces.append({"path": path, "start": start, "end": end})
    return pieces


def generate_complete_chunk(model, chunk: dict, prompt_text: str, temp_dir: str, stats: dict, depth: int = 0, first_limit: int = 0):
    duration = chunk["end"] - chunk["start"]
    limit = first_limit or auto_token_budget(duration)
    tried = set()
    while limit not in tried:
        tried.add(limit)
        stats["max_token_budget_used"] = max(stats["max_token_budget_used"], limit)
        answers = model.generate(prompts=generation_prompt(prompt_text, chunk["path"]), max_new_tokens=limit)
        answer = answers[0]
        if generation_completed(answer, model.text_eos_id):
            return [(chunk, decode_answer(model, answer))]
        next_limit = min(AUTO_MAX_NEW_TOKENS, max(limit + 256, limit * 2))
        if next_limit == limit:
            break
        stats["token_retries"] += 1
        limit = next_limit

    if depth < AUTO_MAX_SPLIT_DEPTH and duration >= 10:
        children = split_chunk(chunk, temp_dir)
        if children:
            stats["token_splits"] += 1
            completed = []
            for child in children:
                completed.extend(generate_complete_chunk(model, child, prompt_text, temp_dir, stats, depth + 1))
            return completed
    raise RuntimeError("Automatic token recovery was exhausted without an end marker; choose a shorter chunk duration or a different model")


def build_prompt(model, prompt: str, context: str = "") -> str:
    prompt = prompt.strip() or "Transcribe the following:"
    if context.strip():
        # Context is reference vocabulary, not text to invent in the transcript.
        context = context.replace(model.audio_locator_tag, " ")
        prompt = ("Use the following background only to disambiguate words heard in the audio. "
                  "Do not include these notes or invent speech.\n"
                  f"Background: {json.dumps(context.strip(), ensure_ascii=False)}\n" + prompt)
    if model.audio_locator_tag in prompt:
        return prompt
    return f"{prompt} {model.audio_locator_tag}"


def transcribe_audio(
    audio_path: str,
    output_file: str,
    batch_size: int = 1,
    chunk_duration_secs: float = 40,
    max_new_tokens: int = 0,
    device: str = "auto",
    precision: str = "float16",
    prompt: str = "Transcribe the following:",
    timestamps: bool = True,
    context: str = "",
):
    print("Loading NVIDIA Canary-Qwen model: nvidia/canary-qwen-2.5b")
    torch_device = resolve_device(device)
    if device == "auto" and torch_device.type == "cpu":
        precision = "float32"
    with gpu_execution(torch_device):
        model = SALM.from_pretrained("nvidia/canary-qwen-2.5b")
        model = configure_model(model, torch_device, precision)

    print(f"Processing: {audio_path}")
    print(f"Device: {torch_device}")
    print(f"Precision: {precision}")
    print(f"Batch size: {batch_size}")
    print(f"Chunk duration: {chunk_duration_secs}s")
    print(f"Max new tokens: {'Auto' if max_new_tokens == 0 else max_new_tokens}")

    with tempfile.TemporaryDirectory(prefix="canary_qwen_") as temp_dir:
        chunks = split_audio_file(audio_path, chunk_duration_secs, temp_dir)
        print(f"Created {len(chunks)} chunks")

        prompt_text = build_prompt(model, prompt, context)
        full_text = []
        segments = []
        stats = {"max_token_budget_used": 0, "token_retries": 0, "token_splits": 0}

        with gpu_execution(torch_device), torch.inference_mode():
            for batch in batched(chunks, max(1, batch_size)):
                prompts = [
                    [{
                        "role": "user",
                        "content": prompt_text,
                        "audio": [chunk["path"]],
                    }]
                    for chunk in batch
                ]
                generation_limit = max_new_tokens or max(auto_token_budget(chunk["end"] - chunk["start"]) for chunk in batch)
                stats["max_token_budget_used"] = max(stats["max_token_budget_used"], generation_limit)
                answer_ids = model.generate(
                    prompts=prompts,
                    max_new_tokens=generation_limit,
                )

                for chunk, answer in zip(batch, answer_ids):
                    completed = [(chunk, decode_answer(model, answer))]
                    if not generation_completed(answer, model.text_eos_id):
                        if max_new_tokens > 0:
                            raise RuntimeError("Generation reached its configured token limit without an end marker; increase max_new_tokens or shorten the audio window")
                        stats["token_retries"] += 1
                        retry_limit = min(AUTO_MAX_NEW_TOKENS, max(generation_limit + 256, generation_limit * 2))
                        completed = generate_complete_chunk(model, chunk, prompt_text, temp_dir, stats, first_limit=retry_limit)
                    for completed_chunk, text in completed:
                        full_text.append(text)
                        if timestamps:
                            segments.append({
                                "start": completed_chunk["start"],
                                "end": completed_chunk["end"],
                                "text": text,
                            })
                    print(f"Chunk {chunks.index(chunk) + 1}/{len(chunks)} complete: {sum(len(text) for _, text in completed)} characters")

    final_text = " ".join(part for part in full_text if part).strip()

    output_data = {
        "text": final_text,
        "language": "en",
        "resolved_device": str(torch_device),
        "precision": precision,
        "segments": segments,
        "word_timestamps": [],
        "model": "nvidia/canary-qwen-2.5b",
        "batch_size": batch_size,
        "chunk_duration_secs": chunk_duration_secs,
        "max_new_tokens": max_new_tokens,
        "token_budget_mode": "auto" if max_new_tokens == 0 else "fixed",
        "max_token_budget_used": stats["max_token_budget_used"],
        "token_retries": stats["token_retries"],
        "token_splits": stats["token_splits"],
        "has_word_timestamps": False,
    }

    with open(output_file, "w", encoding="utf-8") as f:
        json.dump(output_data, f, indent=2, ensure_ascii=False)

    print(f"Results saved to: {output_file}")


def main():
    parser = argparse.ArgumentParser(
        description="Transcribe audio using NVIDIA Canary-Qwen"
    )
    parser.add_argument("audio_file", help="Path to audio file")
    parser.add_argument("--output", "-o", required=True, help="Output file path")
    parser.add_argument("--batch-size", type=int, default=1, help="Batch size for chunk generation")
    parser.add_argument("--chunk-len", type=float, default=40, help="Chunk duration in seconds")
    parser.add_argument("--max-new-tokens", type=int, default=0, help="Maximum generated tokens per chunk; zero enables automatic budgeting and recovery")
    parser.add_argument("--device", choices=["auto", "cuda", "cpu"], default="auto", help="Inference device")
    parser.add_argument(
        "--precision",
        choices=["float16", "bfloat16", "float32"],
        default="float16",
        help="Model precision",
    )
    parser.add_argument(
        "--prompt",
        default="Transcribe the following:",
        help="Prompt text. The audio locator is appended automatically if omitted.",
    )
    parser.add_argument("--context", default="", help="Background vocabulary used only to disambiguate audible speech")
    parser.add_argument("--timestamps", action="store_true", default=True, help="Include chunk-level timestamps")
    parser.add_argument("--no-timestamps", dest="timestamps", action="store_false", help="Disable chunk-level timestamps")

    args = parser.parse_args()

    if not os.path.exists(args.audio_file):
        print(f"Error: Audio file not found: {args.audio_file}")
        sys.exit(1)

    try:
        transcribe_audio(
            audio_path=args.audio_file,
            output_file=args.output,
            batch_size=args.batch_size,
            chunk_duration_secs=args.chunk_len,
            max_new_tokens=args.max_new_tokens,
            device=args.device,
            precision=args.precision,
            prompt=args.prompt,
            context=args.context,
            timestamps=args.timestamps,
        )
    except Exception as exc:
        print(f"Error during transcription: {exc}")
        sys.exit(1)


if __name__ == "__main__":
    main()
