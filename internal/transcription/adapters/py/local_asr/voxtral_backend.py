"""Native Transformers Voxtral Mini 3B transcription on bounded windows."""
from backends import RecognitionError, ensure_generation_complete, generation_budget, to_device


def create_backend(model_id, device, dtype, config):
    import torch
    from transformers import AutoProcessor, VoxtralForConditionalGeneration

    processor = AutoProcessor.from_pretrained(model_id)
    model = VoxtralForConditionalGeneration.from_pretrained(model_id, dtype=dtype, attn_implementation="sdpa").to(device).eval()

    class VoxtralBackend:
        def transcribe(self, audio, sample_rate=16000):
            if sample_rate != 16000:
                raise RecognitionError("Voxtral requires 16 kHz audio.")
            language = config.get("language", "en")
            inputs = processor.apply_transcription_request(
                audio=audio, model_id=model_id,
                language=None if language == "auto" else language,
                sampling_rate=sample_rate, format="WAV", return_tensors="pt",
            )
            inputs = to_device(inputs, device, dtype)
            with torch.inference_mode():
                limit = generation_budget(config, len(audio) / sample_rate)
                output = model.generate(**inputs, max_new_tokens=limit, do_sample=False)
            generated = output[:, inputs["input_ids"].shape[1]:]
            ensure_generation_complete(generated[0], limit, model.generation_config.eos_token_id)
            text = processor.batch_decode(generated, skip_special_tokens=True)[0].strip()
            return {"text": text, "language": language}

    return VoxtralBackend()
