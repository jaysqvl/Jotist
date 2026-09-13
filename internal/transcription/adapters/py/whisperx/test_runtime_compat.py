"""Exercise the installed runtime APIs without downloading/loading any model."""
import tempfile
from pathlib import Path
import unittest
import wave

import torch
import torchaudio
import torchcodec

from whisperx.asr import FasterWhisperPipeline


class FakeRecognitionModel:
    def generate_segment_batched(self, features, tokenizer, options):
        return {"text": [str(int(row[0])) for row in features], "avg_logprob": [-0.25] * len(features)}


class RuntimeCompatibilityTests(unittest.TestCase):
    def test_transformers_pipeline_keeps_whisperx_batch_results(self):
        pipeline = FasterWhisperPipeline(FakeRecognitionModel(), None, {}, None, device="cpu")
        pipeline.preprocess = lambda item: {"inputs": torch.tensor([item["value"]])}
        result = list(pipeline(({"value": value} for value in (1, 2, 3)), batch_size=2, num_workers=0))
        self.assertEqual([row["text"] for row in result], ["1", "2", "3"])
        self.assertEqual([row["avg_logprob"] for row in result], [-0.25, -0.25, -0.25])

    def test_torchaudio_resampling_and_alignment_bundle_api(self):
        samples = torchaudio.functional.resample(torch.zeros(1, 160), 16000, 8000)
        self.assertEqual(tuple(samples.shape), (1, 80))
        self.assertIn("WAV2VEC2_ASR_BASE_960H", torchaudio.pipelines.__all__)
        # Reading bundle metadata does not load/download its model.
        self.assertEqual(torchaudio.pipelines.WAV2VEC2_ASR_BASE_960H.sample_rate, 16000)

    def test_torchcodec_decodes_a_generated_pcm_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "silence.wav"
            with wave.open(str(path), "wb") as destination:
                destination.setnchannels(1)
                destination.setsampwidth(2)
                destination.setframerate(16000)
                destination.writeframes(b"\0\0" * 160)
            decoded = torchcodec.decoders.AudioDecoder(str(path)).get_all_samples()
            self.assertEqual(decoded.sample_rate, 16000)
            self.assertEqual(tuple(decoded.data.shape), (1, 160))


if __name__ == "__main__":
    unittest.main()
