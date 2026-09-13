"""Integrity tests use in-memory fixture bytes, never a model or network."""
import hashlib
import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


source = Path(__file__).parent / "vendor/whisperx/whisperx/vad_asset.py"
spec = importlib.util.spec_from_file_location("vad_asset", source)
asset = importlib.util.module_from_spec(spec)
spec.loader.exec_module(asset)


class VADAssetTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.payload = b"a fixed checkpoint fixture"
        self.digest = hashlib.sha256(self.payload).hexdigest()
        for name, value in (("VAD_SIZE", len(self.payload)), ("VAD_SHA256", self.digest)):
            modifier = patch.object(asset, name, value)
            modifier.start()
            self.addCleanup(modifier.stop)

    def test_valid_download_is_atomic_and_reused_without_network(self):
        with patch.object(asset, "urlopen", return_value=io.BytesIO(self.payload)) as download:
            target = Path(asset.cached_vad_checkpoint(self.root))
        download.assert_called_once_with(asset.VAD_URL, timeout=60)
        self.assertEqual(target.read_bytes(), self.payload)
        self.assertEqual(list(target.parent.glob(".download-*")), [])
        with patch.object(asset, "urlopen", side_effect=AssertionError("cache must avoid network")):
            self.assertEqual(asset.cached_vad_checkpoint(self.root), str(target))

    def test_corrupt_or_oversized_download_is_never_published(self):
        for payload in (b"x" * len(self.payload), self.payload + b"extra"):
            with self.subTest(payload=payload), patch.object(asset, "urlopen", return_value=io.BytesIO(payload)):
                with self.assertRaises(RuntimeError):
                    asset.cached_vad_checkpoint(self.root)
            self.assertEqual(list((self.root / "jotist-whisperx-vad").iterdir()), [])

    def test_modified_cache_and_cache_symlinks_fail_before_loading(self):
        cache = self.root / "jotist-whisperx-vad"
        cache.mkdir()
        target = cache / (self.digest + ".bin")
        target.write_bytes(b"x" * len(self.payload))
        with patch.object(asset, "urlopen", side_effect=AssertionError("must fail closed")):
            with self.assertRaisesRegex(RuntimeError, "integrity"):
                asset.cached_vad_checkpoint(self.root)
        target.unlink()
        outside = self.root / "outside"
        outside.write_bytes(self.payload)
        target.symlink_to(outside)
        with self.assertRaisesRegex(RuntimeError, "symbolic link"):
            asset.cached_vad_checkpoint(self.root)
        target.unlink()
        cache.rmdir()
        cache.symlink_to(self.root)
        with self.assertRaisesRegex(RuntimeError, "symbolic link"):
            asset.cached_vad_checkpoint(self.root)

    def test_offline_mode_never_attempts_a_missing_download(self):
        with patch.dict("os.environ", {"HF_HUB_OFFLINE": "1"}), patch.object(asset, "urlopen", side_effect=AssertionError("offline")):
            with self.assertRaisesRegex(RuntimeError, "offline mode"):
                asset.cached_vad_checkpoint(self.root)


if __name__ == "__main__":
    unittest.main()
