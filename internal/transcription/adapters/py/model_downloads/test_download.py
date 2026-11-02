"""Real file operations and official-client boundaries, with no weight network."""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import subprocess
import sys
import unittest
from types import SimpleNamespace
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("model_download", Path(__file__).with_name("download.py"))
download = importlib.util.module_from_spec(spec)
spec.loader.exec_module(download)


class DownloadTests(unittest.TestCase):
    def test_hub_uses_fixed_commit_then_records_branch_without_redownloading(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            root = Path(directory)
            weight = root / "weights.bin"
            weight.write_bytes(b"public synthetic weights")
            info = SimpleNamespace(filename="model.bin", commit_hash="a"*40, file_size=weight.stat().st_size)
            calls = []

            def snapshot(**kwargs):
                calls.append(kwargs)
                return [info] if kwargs.get("dry_run") else str(root / ("a"*40))

            with patch.object(download, "snapshot_download", side_effect=snapshot), patch.object(download, "hf_hub_download", return_value=str(weight)) as fetch:
                progress = download.Progress()
                download.hub_artifact({"source": "publisher/model"}, "secret-test-token", progress)
            self.assertEqual(fetch.call_args.kwargs["revision"], "a"*40)
            self.assertEqual(calls[-1]["allow_patterns"], [])
            self.assertNotIn("dry_run", calls[-1])
            self.assertEqual(progress.done, weight.stat().st_size)
            self.assertEqual(progress.completed, 1)

    def test_direct_file_is_hardlinked_without_a_second_weight_copy(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            source = root / "cache" / "weights"
            source.parent.mkdir()
            source.write_bytes(b"complete weights")
            target = root / "runtime" / "model.nemo"
            download.link_weight(source, target)
            self.assertEqual(os.stat(source).st_ino, os.stat(target).st_ino)
            self.assertEqual(target.read_bytes(), b"complete weights")

    def test_kept_existing_destination_is_not_overwritten(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            target = Path(directory).resolve() / "model.nemo"
            target.write_bytes(b"existing kept weights")
            info = SimpleNamespace(filename="model.nemo", commit_hash="a"*40, file_size=target.stat().st_size)
            with patch.object(download, "snapshot_download", return_value=[info]), patch.object(download, "hf_hub_download", side_effect=AssertionError("should not fetch weights")):
                download.hub_artifact({"source": "publisher/model", "destination": str(target), "root": str(target.parent), "minimum_size": 3}, None, download.Progress())
            self.assertEqual(target.read_bytes(), b"existing kept weights")

    def test_large_but_incomplete_direct_weight_is_not_treated_as_downloaded(self):
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            root = Path(directory).resolve()
            target = root / "model.nemo"
            target.write_bytes(b"partial but above minimum")
            complete = root / "complete-model"
            complete.write_bytes(b"complete public synthetic weights with a longer payload")
            info = SimpleNamespace(filename="model.nemo", commit_hash="a"*40, file_size=complete.stat().st_size)
            with patch.object(download, "snapshot_download", side_effect=[[info], str(root/("a"*40))]), patch.object(download, "hf_hub_download", return_value=str(complete)) as fetch:
                download.hub_artifact({"source": "publisher/model", "destination": str(target), "root": str(target.parent), "minimum_size": 3}, None, download.Progress())
            fetch.assert_called_once()
            self.assertEqual(target.read_bytes(), complete.read_bytes())

    def test_symlink_and_corrupt_checkpoint_are_rejected_without_clobbering(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            kept = root / "kept"
            kept.write_bytes(b"preserve")
            target = root / "alias"
            target.symlink_to(kept)
            with self.assertRaises(ValueError):
                download.valid_file(target, {})
            with self.assertRaises(ValueError):
                download.valid_file(kept, {"sha256": "0"*64})
            self.assertEqual(kept.read_bytes(), b"preserve")

    def test_progress_is_numeric_and_never_contains_token_or_filename(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            progress = download.Progress()
            progress.files = 1
            progress.total = 100
            progress.report(current=200, force=True)
        value = json.loads(output.getvalue())
        self.assertEqual(value["downloaded_bytes"], 100)
        self.assertEqual(set(value), {"type", "phase", "downloaded_bytes", "total_bytes", "completed_files", "total_files"})

    def test_disk_full_is_an_actionable_public_error(self):
        import errno
        self.assertEqual(download.classify(OSError(errno.ENOSPC, "private path")), "disk_full")
        self.assertEqual(download.classify(ValueError("secret token")), "download_failed")

    def test_download_cannot_consume_recording_space_reserve(self):
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(download.shutil, "disk_usage", return_value=SimpleNamespace(free=download.RECORDING_SPACE_RESERVE+10)):
                with self.assertRaises(download.InsufficientSpaceError):
                    download.ensure_space(directory, 11)
                download.ensure_space(directory, 10)
        self.assertEqual(download.classify(download.InsufficientSpaceError()), "storage_low")

    def test_configured_root_alias_works_but_child_symlinks_cannot_escape(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            real = root / "real"
            real.mkdir()
            alias = root / "configured-alias"
            alias.symlink_to(real, target_is_directory=True)
            self.assertEqual(download.destination_parent(alias / "weights", alias), real / "weights")
            (real / "escape").symlink_to(root, target_is_directory=True)
            with self.assertRaises(ValueError):
                download.destination_parent(alias / "escape" / "weights", alias)

    def test_child_exits_when_its_owning_server_pipe_closes(self):
        code = """import importlib.util,sys,time
spec=importlib.util.spec_from_file_location('worker',sys.argv[1])
worker=importlib.util.module_from_spec(spec);spec.loader.exec_module(worker)
worker.hub_artifact=lambda *args: time.sleep(60)
worker.main()
"""
        process = subprocess.Popen([sys.executable, "-I", "-c", code, str(Path(__file__).with_name("download.py"))], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            process.stdin.write(json.dumps({"watch_parent": True, "artifacts": [{"source": "synthetic/unused"}]})+"\n")
            process.stdin.flush()
            self.assertEqual(json.loads(process.stdout.readline())["type"], "progress")
            process.stdin.close()
            self.assertEqual(process.wait(timeout=5), 130)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()
            process.stderr.close()

    def test_completed_child_exits_normally_while_parent_pipe_stays_open(self):
        code = """import importlib.util,sys
spec=importlib.util.spec_from_file_location('worker',sys.argv[1])
worker=importlib.util.module_from_spec(spec);spec.loader.exec_module(worker)
worker.main()
"""
        process = subprocess.Popen([sys.executable, "-I", "-c", code, str(Path(__file__).with_name("download.py"))], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            process.stdin.write(json.dumps({"watch_parent": True, "artifacts": []})+"\n")
            process.stdin.flush()
            self.assertEqual(process.wait(timeout=5), 0)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdin.close()
            process.stdout.close()
            process.stderr.close()


if __name__ == "__main__":
    unittest.main()
