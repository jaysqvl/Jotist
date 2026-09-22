"""Publisher inference APIs, isolated from NeMo and hosted transcription APIs.

Model/code revisions live in models.json. Only approved catalog checkpoints
can execute remote code. Audio arrays and prompts never leave this process;
Hugging Face is used solely to retrieve/cache the requested model artifacts.
"""
from __future__ import annotations

import re


COHERE_AUTO_INITIAL_TOKENS = 768
COHERE_AUTO_MAX_TOKENS = 1000
COHERE_CONTEXT_RESERVE = 24
GENERATION_TOKEN_LIMIT_MESSAGE = "Generation reached its token limit; increase max_new_tokens or shorten the audio window."
COHERE_AUTO_LIMIT_MESSAGE = "Cohere reached its Auto output limit without an end marker; shorten chunk_duration or choose a different model."


class RecognitionError(RuntimeError):
    """An application-owned diagnostic safe to show without library secrets."""


class GenerationTokenLimitError(RecognitionError):
    """A generated window ended at its output budget without an end marker."""

    def __init__(self, limit):
        self.token_limit = limit
        super().__init__(GENERATION_TOKEN_LIMIT_MESSAGE)


class CohereAutoTokenLimitError(RecognitionError):
    """The Cohere Auto budget reached the decoder's safe ceiling."""

    def __init__(self, limit):
        self.token_limit = limit
        super().__init__(COHERE_AUTO_LIMIT_MESSAGE)


def vocabulary(config):
    return list(dict.fromkeys(line.strip() for line in config.get("context_terms", "").splitlines() if line.strip()))


def generation_budget(config, seconds, rich=False):
    explicit = int(config.get("max_new_tokens", 0))
    return explicit or min(65536, max(512, int(seconds * (12 if rich else 8)) + 256))


def ensure_generation_complete(ids, limit, eos_ids):
    """Never silently save a generation stopped only by max_new_tokens."""
    if len(ids) < limit:
        return
    eos = {eos_ids} if isinstance(eos_ids, int) else set(eos_ids or [])
    last = int(ids[-1]) if len(ids) else None
    if last not in eos:
        raise GenerationTokenLimitError(limit)


def to_device(inputs, device, dtype):
    # BatchFeature.to(dtype=...) must not cast input IDs or length tensors.
    import torch
    for name, value in inputs.items():
        if torch.is_tensor(value):
            inputs[name] = value.to(device=device, dtype=dtype if value.is_floating_point() else value.dtype)
    return inputs


def prepare_moss_preview_inputs(self, input_ids, next_sequence_length=None, past_key_values=None,
                                attention_mask=None, inputs_embeds=None, is_first_iteration=False, **kwargs):
    """Use Transformers 5 cache slicing, retaining custom audio only at prefill.

    The pinned preview checks the removed cache_position generation argument,
    so it reuses its original audio mask against a growing text sequence. The
    library prepares current token/cache positions; only its three custom audio
    inputs need the same prefill-only rule as built-in multimodal inputs.
    """
    from transformers.generation.utils import GenerationMixin
    inputs = GenerationMixin.prepare_inputs_for_generation(
        self, input_ids, next_sequence_length=next_sequence_length,
        past_key_values=past_key_values, attention_mask=attention_mask,
        inputs_embeds=inputs_embeds, is_first_iteration=is_first_iteration, **kwargs,
    )
    if not is_first_iteration and kwargs.get("use_cache", True):
        for name in ("audio_data", "audio_data_seqlens", "audio_input_mask"):
            inputs.pop(name, None)
    return inputs


def parse_moss(text):
    pattern = re.compile(r"\[(\d+(?:\.\d+)?)\]\[(S\d+)\](.*?)\[(\d+(?:\.\d+)?)\]", re.S)
    segments, cursor = [], 0
    for match in pattern.finditer(text):
        if text[cursor:match.start()].strip():
            raise RecognitionError("MOSS returned unparsed content between timestamped segments.")
        start, speaker, content, end = match.groups()
        start, end = float(start), float(end)
        if end < start:
            raise RecognitionError("MOSS returned a segment with reversed timestamps.")
        if content.strip():
            segments.append({"start": start, "end": end, "text": content.strip(), "speaker": speaker})
        cursor = match.end()
    if text[cursor:].strip():
        raise RecognitionError("MOSS returned an incomplete or unrecognised timestamped transcript.")
    return segments


def parse_granite_timestamps(text):
    """Granite emits word ends modulo 10s; starts are previous emitted ends.

    Silence '_' tags advance the clock without introducing transcript words.
    These are model timing predictions, not invented evenly spaced words.
    """
    words, cursor, last_end, offset = [], 0, 0.0, 0.0
    for match in re.finditer(r"\[T:(\d+)\]", text):
        word = text[cursor:match.start()].strip()
        cursor = match.end()
        end = int(match.group(1)) / 100.0
        while end + offset < last_end:
            offset += 10.0
        end += offset
        if word and word != "_":
            words.append({"word": word, "start": last_end, "end": end})
        last_end = end
    if text[cursor:].strip() or (text.strip() and not words):
        raise RecognitionError("Granite Plus returned an incomplete word timestamp transcript.")
    return words


class TransformersBackend:
    def __init__(self, model_id, device, dtype, config):
        import torch
        from transformers import AutoModelForCausalLM, AutoModelForCTC, AutoModelForSpeechSeq2Seq, AutoProcessor, AutoTokenizer

        self.model_id, self.device, self.dtype, self.config = model_id, device, dtype, config
        self.engine = config["engine"]
        remote = self.engine in {"ark", "moss", "moss_preview"}
        kwargs = {"trust_remote_code": True, "revision": config["revision"]} if remote else {}
        self.kwargs = kwargs

        if self.engine == "moss_preview":
            from huggingface_hub import hf_hub_download
            from transformers.dynamic_module_utils import get_class_from_dynamic_module
            model_class = get_class_from_dynamic_module("modeling_Moss.MossForCausalLM", model_id, revision=config["revision"])
            model_kwargs = dict(kwargs)
            if (model_id == "OpenMOSS-Team/MOSS-Transcribe-preview-2B"
                    and config["revision"] == "c98175cb20e48bd9be4e95f6c85f2af18899f780"):
                # This pinned publisher implementation uses the pre-v5 list
                # format. Transformers 5 requires the destination/source map.
                # Its own tie_weights() aliases exactly these two parameters.
                if getattr(model_class, "_tied_weights_keys", None) == ["lm_head.weight"]:
                    model_class._tied_weights_keys = {"lm_head.weight": "model.language_model.embed_tokens.weight"}
                # The published top-level flag is false, although its own
                # tie_weights() unconditionally aliases the output head. Make
                # that declared contract visible to v5's checkpoint loader.
                model_kwargs["tie_word_embeddings"] = True
                model_class.prepare_inputs_for_generation = prepare_moss_preview_inputs
            self.model = model_class.from_pretrained(model_id, dtype=dtype, attn_implementation="sdpa", **model_kwargs).to(device).eval()
            tokenizer = AutoTokenizer.from_pretrained(model_id, **kwargs)
            dynamic = {"revision": config["revision"]}
            processor_class = get_class_from_dynamic_module("processing_Moss.MossProcessor", model_id, **dynamic)
            mel_class = get_class_from_dynamic_module("processing_Moss.MelConfig", model_id, **dynamic)
            self.processor = processor_class(tokenizer, config=mel_class(mel_sr=16000, mel_dim=128, mel_n_fft=400, mel_hop_length=160), enable_time_marker=False)
            self.processor.load_template(hf_hub_download(model_id, "chat_template_default.py", revision=config["revision"]))
            self.tokenizer = tokenizer
            return

        self.processor = AutoProcessor.from_pretrained(model_id, **kwargs)
        if self.engine == "cohere":
            from transformers import CohereAsrForConditionalGeneration
            model_class = CohereAsrForConditionalGeneration
        elif self.engine == "granite_ctc":
            model_class = AutoModelForCTC
        elif self.engine in {"granite", "granite_plus"}:
            model_class = AutoModelForSpeechSeq2Seq
        else:
            model_class = AutoModelForCausalLM
        self.model = model_class.from_pretrained(model_id, dtype=dtype, **kwargs).to(device).eval()
        self.tokenizer = self.processor.tokenizer
        self.torch = torch

    def transcribe(self, audio, sample_rate=16000):
        import torch
        seconds = len(audio) / sample_rate
        language = self.config.get("language", "en")
        limit = generation_budget(self.config, seconds, self.engine in {"moss", "granite_plus"})

        if self.engine == "cohere":
            inputs = self.processor(audio, sampling_rate=sample_rate, return_tensors="pt", language=language)
            inputs = to_device(inputs, self.device, self.dtype)
            auto = not int(self.config.get("max_new_tokens", 0))
            retry_limit = limit
            if auto:
                # This model's decoder advertises a finite sequence length.
                # Leave room for its start/special tokens and cap the retry;
                # a fixed user budget must remain exact.
                sequence_length = getattr(getattr(self.model, "config", None), "max_seq_len", None)
                if isinstance(sequence_length, int) and sequence_length > COHERE_CONTEXT_RESERVE:
                    retry_limit = min(COHERE_AUTO_MAX_TOKENS, sequence_length - COHERE_CONTEXT_RESERVE)
                    limit = min(retry_limit, max(COHERE_AUTO_INITIAL_TOKENS, limit, getattr(self, "_cohere_auto_budget", 0)))
            for budget in (limit, retry_limit) if auto and retry_limit > limit else (limit,):
                with torch.inference_mode():
                    output = self.model.generate(**inputs, max_new_tokens=budget, do_sample=False)
                try:
                    ensure_generation_complete(output[0], budget, self.model.generation_config.eos_token_id)
                except GenerationTokenLimitError as exc:
                    if auto and budget < retry_limit:
                        continue
                    if auto:
                        raise CohereAutoTokenLimitError(budget) from exc
                    raise
                if auto and budget > limit:
                    # A dense window needed the larger budget; avoid repeating
                    # its first failed generation on later windows in this run.
                    self._cohere_auto_budget = budget
                break
            text = self.processor.decode(output, skip_special_tokens=True)
            if isinstance(text, list):
                text = " ".join(text)
            return {"text": text.strip(), "language": language}

        if self.engine == "granite_ctc":
            inputs = self.processor([audio], sampling_rate=sample_rate, device=self.device)
            inputs = to_device(inputs, self.device, self.dtype)
            with torch.inference_mode():
                output = self.model.generate(**inputs)
            return {"text": self.processor.batch_decode(output, skip_special_tokens=True)[0].strip(), "language": language}

        if self.engine in {"granite", "granite_plus"}:
            # The base supports keyword prompts; arbitrary prose instructions
            # are not advertised or silently appended to this specialised model.
            if self.engine == "granite_plus":
                prompt = "Timestamps: Transcribe the speech. After each word, add a timestamp tag showing the end time in centiseconds, e.g. hello [T:45] world [T:82]"
            elif vocabulary(self.config):
                prompt = "transcribe the speech to text."
            else:
                prompt = "transcribe the speech with proper punctuation and capitalization."
            terms = vocabulary(self.config)
            if terms:
                prompt += " Keywords: " + ", ".join(terms)
            messages = [{"role": "user", "content": "<|audio|> " + prompt}]
            if self.engine == "granite_plus":
                messages.insert(0, {"role":"system", "content":"Knowledge Cutoff Date: April 2024.\nToday's Date: December 19, 2024.\nYou are Granite, developed by IBM. You are a helpful AI assistant"})
            text_prompt = self.tokenizer.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)
            inputs = self.processor(text_prompt, audio, device=self.device, return_tensors="pt")
        elif self.engine == "ark":
            messages = [{"role":"user", "content":[{"type":"audio", "array":audio}, {"type":"text", "text":"Please transcribe this audio."}]}]
            inputs = self.processor.apply_chat_template(messages, add_generation_prompt=True, return_tensors="pt", sampling_rate=sample_rate, audio_padding="longest", text_kwargs={"padding":"longest"}, audio_max_length=30 * sample_rate)
        elif self.engine == "moss":
            prompt = ("请将音频转写为文本，每一段需以起始时间戳和说话人编号"
                      "（[S01]、[S02]、[S03]…）开头，正文为对应的语音内容，"
                      "并在段末标注结束时间戳，以清晰标明该段语音范围。")
            if vocabulary(self.config):
                prompt += "热词提示：" + ", ".join(vocabulary(self.config))
            if self.config.get("context", "").strip():
                prompt += "\nContext: " + self.config["context"].strip()
            # Template sees one audio marker; the processor receives the actual
            # waveform directly. No temporary external URL or helper upload.
            messages = [{"role":"user", "content":[{"type":"audio", "audio":"local.wav"}, {"type":"text", "text":prompt}]}]
            rendered = self.processor.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)
            inputs = self.processor(text=rendered, audio=[audio], max_length=131072, return_tensors="pt")
        elif self.engine == "moss_preview":
            inputs = self.processor(audio=audio, return_tensors="pt")
        else:
            raise RecognitionError("Unsupported recognition backend.")

        inputs = to_device(inputs, self.device, self.dtype)
        generation = {"max_new_tokens":limit, "do_sample":False, "num_beams":1}
        eos = self.model.generation_config.eos_token_id
        if self.engine == "ark":
            keep = {self.tokenizer.eos_token_id} if isinstance(self.tokenizer.eos_token_id, int) else set(self.tokenizer.eos_token_id or [])
            bad = set(self.tokenizer.all_special_ids) - keep
            bad.update(token_id for token, token_id in self.tokenizer.get_added_vocab().items() if token.startswith("<") and token.endswith(">") and token_id not in keep)
            generation.update(bad_words_ids=[[token_id] for token_id in sorted(bad)], pad_token_id=self.tokenizer.pad_token_id, eos_token_id=self.tokenizer.eos_token_id)
        elif self.engine == "moss_preview":
            eos = [self.processor.end_token_id]
            generation.update(use_cache=True, eos_token_id=eos)
        if self.engine == "moss":
            available = 131072 - inputs["input_ids"].shape[-1]
            if available < 1 or (self.config.get("max_new_tokens", 0) and limit > available):
                raise RecognitionError("MOSS input plus generation budget exceeds its context window; reduce the audio window or max_new_tokens.")
            # Its documented 90-minute input can leave less than 65536 output
            # slots. Auto budgeting uses the actual remaining context; a real
            # token cutoff is still detected after generation and fails.
            limit = min(limit, available)
            generation["max_new_tokens"] = limit
        with torch.inference_mode():
            outputs = self.model.generate(**inputs, **generation)
        new_ids = outputs[0, inputs["input_ids"].shape[-1]:]
        ensure_generation_complete(new_ids, limit, eos)
        text = self.tokenizer.decode(new_ids, skip_special_tokens=True).strip()
        if self.engine == "moss":
            segments = parse_moss(text)
            return {"text":" ".join(segment["text"] for segment in segments), "segments":segments, "language":language, "timestamp_source":"native", "speaker_scope":"recording"}
        if self.engine == "granite_plus":
            words = parse_granite_timestamps(text)
            return {"text":" ".join(word["word"] for word in words), "word_segments":words, "language":language, "timestamp_source":"native_word_ends_previous_end_starts"}
        return {"text":text, "language":language}


def create_backend(model_id, device, dtype, config):
    if config["engine"] == "qwen":
        from qwen_backend import create_backend as create_qwen
        return create_qwen(model_id, device, dtype, config)
    if config["engine"] in {"voxtral", "voxtral_realtime"}:
        from voxtral_backend import create_backend as create_voxtral
        return create_voxtral(model_id, device, dtype, config)
    return TransformersBackend(model_id, device, dtype, config)
