"""Artifact contracts exercised without installing/loading a speech model."""
import ast
import errno
import hashlib
import io
import json
import math
from pathlib import Path
import shutil
import struct
import tarfile
import tempfile
import unittest
from types import SimpleNamespace


SOURCE = Path(__file__).parents[1] / "canary_stages.py"


def artifact_functions():
    tree = ast.parse(SOURCE.read_text())
    module = ast.Module(body=[node for node in tree.body if isinstance(node, ast.FunctionDef)
                             and node.name in ("sha256_file", "extract_assets", "ctc_window_layout", "restore_audio_cut")], type_ignores=[])
    namespace = dict(hashlib=hashlib, Path=Path, json=json, tempfile=tempfile,
                     tarfile=tarfile, shutil=shutil, errno=errno, math=math)
    exec(compile(module, str(SOURCE), "exec"), namespace)
    return namespace


class CanaryArtifactTests(unittest.TestCase):
    def test_subsecond_native_center_padding_must_match_captured_sample_hash(self):
        class Samples(list):
            ndim = 1
            def astype(self, dtype):
                assert dtype == '<f4'
                return self
            def tobytes(self):
                return struct.pack('<' + 'f' * len(self), *self)
        raw = Samples([0.25] * 8000)
        expected = Samples([0.] * 4000 + raw + [0.] * 4000)
        functions = artifact_functions()
        functions['sf'] = SimpleNamespace(read=lambda *args, **kwargs: (raw, 16000))
        functions['np'] = SimpleNamespace(pad=lambda samples, widths: Samples([0.] * widths[0] + samples + [0.] * widths[1]))
        cut = {'start_sample': 0, 'length_samples': 16000, 'sha256': hashlib.sha256(expected.tobytes()).hexdigest()}
        self.assertEqual(functions['restore_audio_cut']('prepared.wav', cut), expected)
        raw[0] = -0.25
        with self.assertRaisesRegex(ValueError, 'differ'):
            functions['restore_audio_cut']('prepared.wav', cut)

    def test_private_audio_temporaries_are_owned_by_the_go_attempt(self):
        tree = ast.parse(SOURCE.read_text())
        calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call)
                 and isinstance(node.func, ast.Attribute) and node.func.attr == "TemporaryDirectory"]
        self.assertEqual(len(calls), 2)
        for call in calls:
            directory = next((item.value for item in call.keywords if item.arg == 'dir'), None)
            self.assertIsNotNone(directory)
            self.assertEqual(ast.unparse(directory), 'Path(args.output).parent')

    def test_ctc_window_centers_cover_every_frame_once_without_changing_recognition(self):
        layout = artifact_functions()["ctc_window_layout"]
        for samples in (1, 1280, 16000 * 15, 16000 * 40, 16000 * 40 - 1):
            for seconds in (10, 20):
                owners = []
                windows = layout(samples, 1280, seconds, 2)
                for start, end, keep_start, keep_end in windows:
                    self.assertLessEqual((end-start)*1280, seconds*16000)
                    self.assertGreater(keep_end, keep_start)
                    owners.extend(range(start+keep_start, start+keep_end))
                    if start > 0:
                        self.assertEqual(keep_start, 25, 'left context is exactly 2 seconds')
                self.assertEqual(owners, list(range(math.ceil(samples/1280))))
        for seconds, overlap in ((5, 2), (10, 1), (10, 5)):
            with self.assertRaises(ValueError):
                layout(640000, 1280, seconds, overlap)

    def archive(self, root, extra=None):
        archive = root / "model.nemo"
        files = {"model_config.yaml": b"main config", "model_weights.ckpt": b"recognizer weights excluded",
                 "timestamps_asr_model_config.yaml": b"ctc config", "timestamps_asr_model_weights.ckpt": b"ctc weights",
                 "tokenizer.model": b"exact tokenizer"}
        files.update(extra or {})
        with tarfile.open(archive, "w") as output:
            for name, data in files.items():
                member = tarfile.TarInfo(name)
                member.size = len(data)
                output.addfile(member, io.BytesIO(data))
        return archive

    def test_alignment_assets_exclude_recognizer_weights_and_reject_tampering(self):
        functions = artifact_functions()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive = self.archive(root)
            assets, digest = functions["extract_assets"](archive, root / "cache")
            self.assertEqual(digest, hashlib.sha256(archive.read_bytes()).hexdigest())
            self.assertFalse((assets / "model_weights.ckpt").exists())
            self.assertEqual((assets / "timestamps_asr_model_weights.ckpt").read_bytes(), b"ctc weights")
            self.assertEqual(functions["extract_assets"](archive, root / "cache"), (assets, digest))
            (assets / "tokenizer.model").write_bytes(b"wrong tokenizer")
            with self.assertRaisesRegex(ValueError, "integrity"):
                functions["extract_assets"](archive, root / "cache")

    def test_nested_archive_member_cannot_escape_or_publish_partial_cache(self):
        functions = artifact_functions()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive = self.archive(root, {"../outside": b"bad"})
            with self.assertRaisesRegex(ValueError, "nested"):
                functions["extract_assets"](archive, root / "cache")
            self.assertEqual(list((root / "cache").iterdir()), [])
            self.assertFalse((root / "outside").exists())


if __name__ == "__main__":
    unittest.main()
