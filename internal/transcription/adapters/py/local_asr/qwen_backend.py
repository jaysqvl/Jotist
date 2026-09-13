"""Native Transformers Qwen3 ASR and forced alignment (no remote code).

The runner owns file decoding, silence-aware chunking, and global offsets.
Factory-local imports keep contract tests independent of model downloads.
"""

import math
import re
import unicodedata
from importlib.util import find_spec

from backends import RecognitionError


def validate_alignment_language(language):
    """Qwen's alignment languages are narrower than the ASR catalog."""
    requested = str(language or "").strip().lower()
    guidance = "Disable word alignment for ASR-only transcription; external diarization requires supported or native timestamps."
    if requested in {"", "auto"}:
        raise RecognitionError("Word alignment has no detected language. Select an explicit supported language or disable word alignment. External diarization requires supported or native timestamps.")
    supported = {"en": "english", "zh": "chinese", "yue": "cantonese", "fr": "french", "de": "german", "it": "italian", "ja": "japanese", "ko": "korean", "pt": "portuguese", "ru": "russian", "es": "spanish"}
    resolved = supported.get(requested, requested)
    if resolved not in supported.values():
        raise RecognitionError("The selected language is supported by some ASR models but not by Qwen word alignment. " + guidance)
    dependency = {"japanese": "nagisa", "korean": "soynlp"}.get(resolved)
    if dependency and find_spec(dependency) is None:
        raise RecognitionError(f"{resolved.title()} word alignment requires the {dependency} tokenizer, which is missing from this worker. " + guidance)
    return resolved.title()


def context_prompt(config):
    prose = str(config.get("context") or "").strip()
    terms = [term.strip() for term in re.split(r"[,\n]", str(config.get("context_terms") or "")) if term.strip()]
    sections = [prose] if prose else []
    if terms:
        sections.append("Vocabulary: " + ", ".join(dict.fromkeys(terms)) + ".")
    return "\n".join(sections) or None


def check_generation_complete(output_ids, max_tokens, eos_token_id):
    """Never save a token-budget cutoff as a complete transcription."""
    if output_ids.shape[-1] < max_tokens:
        return
    eos_ids = eos_token_id if isinstance(eos_token_id, (list, tuple)) else [eos_token_id]
    if int(output_ids[0, -1]) not in eos_ids:
        raise RecognitionError("Transcription reached max_new_tokens; increase the token limit or shorten audio chunks.")


def normalize_alignment(items, duration):
    words = []
    for item in items:
        start, end = float(item["start_time"]), float(item["end_time"])
        if not math.isfinite(start) or not math.isfinite(end) or start < 0 or end < start or end > duration + 0.25:
            raise ValueError("Forced aligner returned timestamps outside the audio chunk")
        words.append({"word": str(item["text"]), "start": min(start, duration), "end": min(end, duration)})
    return words


def restore_alignment_surface(words, transcript):
    """Keep ASR spelling while matching every alignment character in order.

    Transformers' Qwen3 aligner removes punctuation during word tokenization.
    Those cleaned tokens supply timing only; they must not replace technical
    identifiers or punctuation in the transcript shown to the user.
    """
    def kept(character):
        return character == "'" or unicodedata.category(character)[0] in {"L", "N"}

    positions = [index for index, character in enumerate(transcript) if kept(character)]
    expected = "".join(transcript[index] for index in positions)
    tokens = ["".join(character for character in word["word"] if kept(character)) for word in words]
    if not expected or any(not token for token in tokens) or "".join(tokens) != expected:
        raise RecognitionError("Forced alignment did not match the complete transcript; no partial word timing was saved.")

    restored, consumed, raw_start = [], 0, 0
    for word, token in zip(words, tokens):
        first = positions[consumed]
        consumed += len(token)
        last = positions[consumed - 1] + 1
        if any(character.isspace() for character in transcript[first:last]):
            raise RecognitionError("Forced alignment crossed an original word boundary; no guessed word timing was saved.")
        # Closing punctuation stays on the previous word; whitespace and any
        # following opening punctuation delimit the next original surface word.
        boundary = positions[consumed] if consumed < len(positions) else len(transcript)
        if consumed < len(positions):
            boundary = next((index for index in range(last, boundary) if transcript[index].isspace()), boundary)
        restored.append({**word, "word": transcript[raw_start:boundary].strip()})
        raw_start = boundary
    return restored


def create_backend(model_id, device, dtype, config):
    import torch
    from transformers import AutoModelForMultimodalLM, AutoProcessor

    revision = config.get("revision", "main")
    processor = AutoProcessor.from_pretrained(model_id, revision=revision)
    model = AutoModelForMultimodalLM.from_pretrained(
        model_id, revision=revision, dtype=dtype, attn_implementation="sdpa"
    ).to(device).eval()

    class QwenBackend:
        def transcribe(self, audio, sample_rate=16000):
            if sample_rate != 16000:
                raise ValueError("Qwen audio must be resampled to 16 kHz by the runner")
            language = config.get("language") or None
            if language == "auto":
                language = None
            inputs = processor.apply_transcription_request(
                audio=audio, language=language, prompt=context_prompt(config),
                processor_kwargs={"audio_kwargs": {"sampling_rate": sample_rate}},
            ).to(model.device, model.dtype)
            max_tokens = int(config.get("max_new_tokens") or 1024)
            with torch.inference_mode():
                outputs = model.generate(**inputs, max_new_tokens=max_tokens, do_sample=False)
            generated = outputs[:, inputs["input_ids"].shape[1]:]
            check_generation_complete(generated, max_tokens, model.generation_config.eos_token_id)
            parsed = processor.decode(generated, return_format="parsed")[0]
            return {"text": parsed["transcription"].strip(), "language": parsed.get("language") or language or "en"}

    return QwenBackend()


def create_aligner(device, dtype, config):
    import torch
    from transformers import AutoModelForTokenClassification, AutoProcessor

    model_id = "Qwen/Qwen3-ForcedAligner-0.6B-hf"
    revision = config.get("aligner_revision", "main")
    processor = AutoProcessor.from_pretrained(model_id, revision=revision)
    model = AutoModelForTokenClassification.from_pretrained(
        model_id, revision=revision, dtype=dtype, attn_implementation="sdpa"
    ).to(device).eval()

    class QwenAligner:
        def align(self, audio, text, language, sample_rate=16000):
            if not text.strip():
                return []
            language = validate_alignment_language(language)
            if sample_rate != 16000 or len(audio) > 300 * sample_rate:
                raise ValueError("Forced alignment requires 16 kHz audio chunks of at most five minutes")
            inputs, word_lists = processor.prepare_forced_aligner_inputs(
                audio=audio, transcript=text, language=language,
                processor_kwargs={"audio_kwargs": {"sampling_rate": sample_rate}},
            )
            inputs = inputs.to(model.device, model.dtype)
            with torch.inference_mode():
                output = model(**inputs)
            aligned = processor.decode_forced_alignment(
                logits=output.logits, input_ids=inputs["input_ids"], word_lists=word_lists,
                timestamp_token_id=model.config.timestamp_token_id,
            )[0]
            return restore_alignment_surface(normalize_alignment(aligned, len(audio) / sample_rate), text)

    return QwenAligner()
