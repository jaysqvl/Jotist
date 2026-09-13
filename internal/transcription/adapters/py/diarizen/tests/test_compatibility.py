"""Offline API and checkpoint-safety checks; no model weights or inference."""
import io
import os
import pickle
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

os.environ["PYANNOTE_METRICS_ENABLED"] = "false"
os.environ["HF_HUB_OFFLINE"] = "1"

import numpy as np
import soundfile as sf
import torch
from pyannote.audio.core.io import Audio, get_torchaudio_info
from pyannote.audio.core.model import Model
from pyannote.audio.core.task import Problem, Resolution, Specifications
from pyannote.audio.utils.checkpoint import restricted_checkpoint_loading


class UnexpectedCheckpointType:
    pass


class CompatibilityTests(unittest.TestCase):
    def test_audio_metadata_and_decoder_preserve_samples(self):
        buffer = io.BytesIO()
        sf.write(buffer, np.zeros((320, 2), dtype=np.float32), 16000,
                 format="WAV", subtype="PCM_16")
        buffer.seek(0)
        metadata = get_torchaudio_info({"audio": buffer})
        self.assertEqual((metadata.sample_rate, metadata.num_frames, metadata.num_channels,
                          metadata.bits_per_sample, metadata.encoding), (16000, 320, 2, 16, "PCM_S"))
        self.assertEqual(buffer.tell(), 0)
        waveform, rate = Audio(sample_rate=16000, mono="downmix")(buffer)
        self.assertEqual(tuple(waveform.shape), (1, 320))
        self.assertEqual(rate, 16000)
        self.assertTrue(torch.equal(waveform, torch.zeros_like(waveform)))

    def test_allowed_checkpoint_types_are_scoped(self):
        previous = torch.serialization.get_safe_globals()
        specification = Specifications(Problem.MONO_LABEL_CLASSIFICATION, Resolution.FRAME, 5.0,
                                       classes=["speaker"], powerset_max_classes=1)
        buffer = io.BytesIO()
        torch.save({"specification": specification}, buffer)
        buffer.seek(0)
        with restricted_checkpoint_loading():
            restored = torch.load(buffer, weights_only=True)
        self.assertEqual(restored["specification"], specification)
        self.assertEqual(torch.serialization.get_safe_globals(), previous)

    def test_model_rejects_unapproved_pickle_types(self):
        with tempfile.TemporaryDirectory() as directory:
            checkpoint = Path(directory) / "unapproved.pt"
            torch.save({"unexpected": UnexpectedCheckpointType()}, checkpoint)
            previous = torch.serialization.get_safe_globals()
            with self.assertRaises(pickle.UnpicklingError):
                Model.from_pretrained(checkpoint)
            self.assertEqual(torch.serialization.get_safe_globals(), previous)

    def test_checkpoint_cannot_import_an_unbundled_architecture(self):
        with tempfile.TemporaryDirectory() as directory:
            checkpoint = Path(directory) / "unbundled.pt"
            torch.save({"pyannote.audio": {"architecture": {"module": "os", "class": "system"}}}, checkpoint)
            with self.assertRaisesRegex(ValueError, "bundled pyannote models"):
                Model.from_pretrained(checkpoint)

    def test_hub_download_uses_current_token_argument(self):
        with tempfile.TemporaryDirectory() as directory:
            checkpoint = Path(directory) / "unbundled.pt"
            torch.save({"pyannote.audio": {"architecture": {"module": "os", "class": "system"}}}, checkpoint)
            calls = []

            def download(repo_id, filename, *, repo_type, revision, library_name,
                         library_version, cache_dir, token):
                calls.append((repo_id, token))
                return str(checkpoint)

            with patch("pyannote.audio.core.model.hf_hub_download", download):
                with self.assertRaisesRegex(ValueError, "bundled pyannote models"):
                    Model.from_pretrained("fixture/model", use_auth_token="fixture-token")
            self.assertEqual(calls, [("fixture/model", "fixture-token")] * 2)


if __name__ == "__main__":
    unittest.main()
