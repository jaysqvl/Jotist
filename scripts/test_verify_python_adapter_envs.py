"""Guards for the runtime verifier: reject mismatched native wheel sources."""

import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace
from dataclasses import replace

spec = importlib.util.spec_from_file_location(
    "adapter_verifier", Path(__file__).with_name("verify-python-adapter-envs.py")
)
verifier = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = verifier
spec.loader.exec_module(verifier)


class NativeWheelPolicyTests(unittest.TestCase):
    def test_exact_public_pin_accepts_backend_wheels_but_rejects_other_releases(self):
        adapter = replace(
            verifier.ADAPTERS_BY_KEY["local-asr"],
            package_pins=(verifier.PackagePin("torch", version="2.14.0"),),
        )
        for version in ("2.14.0", "2.14.0+cpu", "2.14.0+cu126", "2.14.0+cu130"):
            with self.subTest(version=version):
                verifier.check_package_pins(adapter, {"torch": [version]})
        for version in ("2.13.0", "2.14.0rc1", "2.14.1", "2.15.0"):
            with self.subTest(version=version):
                with self.assertRaises(verifier.CheckError):
                    verifier.check_package_pins(adapter, {"torch": [version]})

    def test_every_runtime_checks_native_companion_sources(self):
        for adapter in verifier.ADAPTERS:
            with self.subTest(adapter=adapter.key):
                verifier.validate_pyproject(adapter)
                self.assertIn("torchcodec", adapter.torch_companions)
                self.assertIn("torchaudio", adapter.torch_companions)

    def test_mismatched_codec_source_is_rejected(self):
        adapter = verifier.ADAPTERS_BY_KEY["local-asr"]
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory) / "pyproject.toml"
            content = adapter.pyproject.read_text()
            start = content.index("torchcodec = [")
            stop = content.index("]", start) + 1
            content = (
                content[:start]
                + 'torchcodec = { index = "unreviewed" }'
                + content[stop:]
            )
            project.write_text(content)
            with self.assertRaisesRegex(verifier.CheckError, "torchcodec must use"):
                verifier.validate_pyproject(replace(adapter, pyproject=project))

    def test_unreviewed_dependency_override_is_rejected(self):
        adapter = verifier.ADAPTERS_BY_KEY["local-asr"]
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory) / "pyproject.toml"
            project.write_text(
                adapter.pyproject.read_text()
                + '\n[tool.uv]\noverride-dependencies = ["torch==2.5.1"]\n'
            )
            with self.assertRaisesRegex(verifier.CheckError, "overrides differ"):
                verifier.validate_pyproject(replace(adapter, pyproject=project))

    def test_security_pin_cannot_be_left_to_fresh_transitive_resolution(self):
        adapter = verifier.ADAPTERS_BY_KEY["pyannote"]
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory) / "pyproject.toml"
            # The package would still be installed through pyannote.audio;
            # without the direct pin an old lock can retain vulnerable 2.5.5.
            project.write_text(
                adapter.pyproject.read_text().replace('    "lightning==2.6.6",\n', "")
            )
            with self.assertRaisesRegex(
                verifier.CheckError, "lightning==2.6.6 must be a direct"
            ):
                verifier.validate_pyproject(replace(adapter, pyproject=project))

    def test_unpatched_upstream_cannot_satisfy_local_compatibility_version(self):
        adapter = replace(
            verifier.ADAPTERS_BY_KEY["whisperx"],
            package_pins=(
                verifier.PackagePin("whisperx", version="3.8.7rc1+jotist.1"),
            ),
        )
        with self.assertRaisesRegex(
            verifier.CheckError, "expected 3.8.7rc1\\+jotist.1"
        ):
            verifier.check_package_pins(adapter, {"whisperx": ["3.8.7rc1"]})

    def test_vendor_sources_and_backend_are_preserved_in_test_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            for key in ("whisperx", "diarizen", "nvidia-asr", "canary-qwen"):
                adapter = verifier.ADAPTERS_BY_KEY[key]
                target = verifier.copy_pyproject(adapter, Path(directory), "cu130")
                sources = verifier.load_toml(target / "pyproject.toml")["tool"]["uv"][
                    "sources"
                ]
                for source in sources.values():
                    if isinstance(source, dict) and "path" in source:
                        self.assertTrue(
                            (target / source["path"] / "pyproject.toml").is_file()
                        )
                originals = list((adapter.pyproject.parent / "vendor").rglob("*.py"))
                self.assertTrue(originals)
                for original in originals:
                    copied = target / original.relative_to(adapter.pyproject.parent)
                    self.assertEqual(original.read_bytes(), copied.read_bytes())
                for caller in adapter.caller_scripts:
                    self.assertEqual(
                        (adapter.pyproject.parent / caller).read_bytes(),
                        (target / caller).read_bytes(),
                    )
                self.assertIn(
                    'url = "https://download.pytorch.org/whl/cu130"',
                    (target / "pyproject.toml").read_text(),
                )
                self.assertFalse(list((target / "vendor").rglob("*.pyc")))

    def test_inference_gate_rejects_changed_fixture_before_running_a_model(self):
        from io import BytesIO
        with tempfile.TemporaryDirectory() as directory:
            args = SimpleNamespace(inference_smoke_binary=Path(directory) / "runtime.test", timeout=30, verbose=False)
            with patch.object(verifier.urllib.request, "urlopen", return_value=BytesIO(b"changed public input")), patch.object(verifier, "run") as run:
                with self.assertRaisesRegex(verifier.CheckError, "checksum differs"):
                    verifier.run_inference_smoke(Path(directory), args)
                run.assert_not_called()

    def test_inference_gate_uses_real_compiled_test_and_installed_cpu_environment(self):
        from io import BytesIO
        import hashlib
        with tempfile.TemporaryDirectory() as directory:
            workdir = Path(directory)
            args = SimpleNamespace(inference_smoke_binary=workdir / "runtime.test", timeout=30, verbose=False)
            data = b"synthetic gate plumbing"
            with patch.object(verifier.urllib.request, "urlopen", return_value=BytesIO(data)), patch.object(verifier, "SPEECH_FIXTURE_SHA256", hashlib.sha256(data).hexdigest()), patch.object(verifier, "run", return_value=SimpleNamespace(stdout="numeric qualification")) as run, patch.dict(verifier.os.environ, {"HF_TOKEN":"private", "JOTIST_RUNTIME_CONTEXT_FILE":"private"}):
                verifier.run_inference_smoke(workdir, args)
                command = run.call_args.args[0]
                environment = run.call_args.kwargs["env"]
                self.assertEqual(command[0], str(args.inference_smoke_binary.resolve()))
                self.assertIn("-test.run=^TestRuntimeQualification$", command)
                self.assertEqual(environment["JOTIST_RUNTIME_ENV"], str(workdir))
                self.assertEqual(environment["JOTIST_RUNTIME_DEVICE"], "cpu")
                self.assertNotIn("HF_TOKEN", environment)
                self.assertNotIn("JOTIST_RUNTIME_CONTEXT_FILE", environment)


if __name__ == "__main__":
    unittest.main()
