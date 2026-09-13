"""Dependency-free regression checks for device selection and output contracts."""
import ast
import builtins
from copy import deepcopy
import importlib.util
import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import sys
from types import SimpleNamespace
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).parent


def load_module(relative):
    spec = importlib.util.spec_from_file_location(relative.replace("/", "_"), ROOT / relative)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def load_function(relative, name, globals_):
    # Legacy runners import ML packages at module scope. Compile the actual
    # helper body without requiring those large runtimes for contract tests.
    globals_.setdefault("gpu_execution", contextlib.nullcontext)
    tree = ast.parse((ROOT / relative).read_text())
    function = next(node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name == name)
    exec(compile(ast.Module(body=[function], type_ignores=[]), relative, "exec"), globals_)
    return globals_[name]


class RuntimeControls(unittest.TestCase):
    def test_gpu_execution_failures_require_actual_gpu_scope(self):
        helper = load_module("runtime_failure.py")
        for device, error, expected in (
            ("cuda", RuntimeError("backend does not support the selected dtype"), "cuda_runtime_error"),
            ("cuda:0", RuntimeError("CUDA out of memory; 1.403 GiB free"), "cuda_out_of_memory"),
            ("cpu", RuntimeError("CUDA out of memory"), None),
            ("cuda", ValueError("wrong configuration"), None),
            ("cuda", RuntimeError("403 Client Error: Forbidden"), None),
            ("cuda", RuntimeError("Failed to load audio: ffmpeg failed"), None),
            ("cuda", RuntimeError("Error opening input.wav"), None),
            ("cuda", RuntimeError("cancelled"), None),
        ):
            with self.subTest(device=device, error=error):
                self.assertEqual(helper.gpu_failure_kind(error, device), expected)
                with contextlib.redirect_stdout(io.StringIO()) as output, self.assertRaises(type(error)):
                    with helper.gpu_execution(device):
                        raise error
                if expected:
                    record = json.loads(output.getvalue().split("=", 1)[1])
                    self.assertEqual(record, {"device": "cuda", "kind": expected})
                    self.assertNotIn(str(error), output.getvalue())
                else:
                    self.assertEqual(output.getvalue(), "")

    def test_host_oom_requires_cpu_and_allocator_evidence(self):
        helper = load_module("runtime_failure.py")
        for device, error, expected in (
            ("cpu", MemoryError("private allocation"), True),
            ("cpu", RuntimeError("DefaultCPUAllocator: not enough memory"), True),
            ("cpu", RuntimeError("generic out of memory"), False),
            ("cpu", RuntimeError("CUDA out of memory"), False),
            ("cuda", MemoryError("host allocation"), False),
        ):
            with self.subTest(device=device, error=error):
                with contextlib.redirect_stdout(io.StringIO()) as output, self.assertRaises(type(error)):
                    with helper.gpu_execution(device):
                        raise error
                if expected:
                    self.assertTrue(output.getvalue().startswith("SCRIBERR_HOST_FAILURE="))
                    self.assertEqual(json.loads(output.getvalue().split("=",1)[1]), {"device":"cpu", "kind":"host_out_of_memory"})
                    self.assertNotIn(str(error), output.getvalue())
                else:
                    self.assertEqual(output.getvalue(), "")

    def test_whisper_auto_cpu_uses_supported_precision(self):
        module = load_module("whisperx/whisperx_run.py")
        for available, requested, precision, expected in ((False,"auto","float16","float32"), (False,"auto","int8_float16","float32"), (False,"auto","int8","int8"), (True,"auto","float16","float16"), (True,"cpu","float32","float32")):
            with self.subTest(available=available, requested=requested, precision=precision), tempfile.TemporaryDirectory() as directory:
                argv = ["runner", "audio.wav", "--device", requested, "--compute_type", precision, "--output_dir", directory]
                observed = []
                fake_cli = SimpleNamespace(cli=lambda: observed.append(list(sys.argv)))
                modules = {"torch": SimpleNamespace(cuda=SimpleNamespace(is_available=lambda:available)), "whisperx": SimpleNamespace(), "whisperx.__main__": fake_cli}
                with patch.dict(sys.modules, modules), patch.object(sys, "argv", argv), patch.dict(os.environ), contextlib.redirect_stdout(io.StringIO()):
                    module.main()
                self.assertEqual(observed[0][observed[0].index("--compute_type")+1],expected)
                self.assertEqual(observed[0][observed[0].index("--device")+1],"cuda" if available and requested=="auto" else "cpu")

    def test_diarization_telemetry_disabled_before_first_ml_import(self):
        class ReachedMLImport(Exception):
            pass

        original_import = builtins.__import__

        def guarded_import(name, *args, **kwargs):
            if name.partition(".")[0] in {"torch", "pyannote", "whisperx", "diarizen"}:
                self.assertEqual(os.environ.get("PYANNOTE_METRICS_ENABLED"), "false")
                raise ReachedMLImport(name)
            return original_import(name, *args, **kwargs)

        for relative in ("pyannote/pyannote_diarize.py", "whisperx/whisperx_run.py", "research/research_diarize.py"):
            with self.subTest(runtime=relative), patch.dict(os.environ, {"PYANNOTE_METRICS_ENABLED": "true"}), patch.object(sys, "argv", ["runner", "--device", "cpu"]):
                with patch("builtins.__import__", side_effect=guarded_import), self.assertRaises(ReachedMLImport):
                    namespace = runpy.run_path(str(ROOT / relative))
                    if relative.startswith("whisperx/"):
                        namespace["main"]()
                    elif relative.startswith("research/"):
                        namespace["run"](SimpleNamespace(engine="suplime", model="rewayai/suplime", device="cpu"))

    def test_pyannote_cached_login_and_track_labels(self):
        class Annotation:
            def __iter__(self):
                raise AssertionError("Annotation iteration returns tracks, not labels")
            def itertracks(self, yield_label):
                self_test.assertTrue(yield_label)
                yield SimpleNamespace(start=0.0, end=1.0, duration=1.0), 7, "SPEAKER_00"
                yield SimpleNamespace(start=0.5, end=1.5, duration=1.0), 8, "SPEAKER_01"

        self_test = self
        calls = []
        class Pipeline:
            @classmethod
            def from_pretrained(cls, model, **kwargs):
                calls.append(kwargs)
                return cls()
            def to(self, device):
                self_test.assertEqual(device, "cpu")
            def __call__(self, audio, **kwargs):
                return SimpleNamespace(speaker_diarization=Annotation())

        namespace = {"json": json, "Path": Path, "deepcopy": deepcopy, "sys": SimpleNamespace(exit=lambda code: self.fail(f"runtime exited: {code}")), "Pipeline": Pipeline, "torch": SimpleNamespace(cuda=SimpleNamespace(is_available=lambda: False), device=lambda value: value)}
        for name in ("resolve_device", "apply_segmentation_thresholds", "iter_speaker_turns", "save_json_format", "diarize_audio"):
            load_function("pyannote/pyannote_diarize.py", name, namespace)
        with tempfile.TemporaryDirectory() as directory, contextlib.redirect_stdout(io.StringIO()):
            output = Path(directory) / "result.json"
            namespace["diarize_audio"]("input.wav", str(output), hf_token=None, output_format="json", device="cpu")
            result = json.loads(output.read_text())
        self.assertEqual(calls, [{}])
        self.assertEqual(result["speakers"], ["SPEAKER_00", "SPEAKER_01"])
        self.assertEqual([segment["speaker"] for segment in result["segments"]], ["SPEAKER_00", "SPEAKER_01"])
        self.assertEqual(result["segments"][1]["start"], 0.5)

    def test_pyannote_powerset_keeps_duration_and_skips_unsupported_thresholds(self):
        parameters = {"segmentation": {"min_duration_off": 0.0}, "clustering": {"threshold": 0.7}}
        pipeline = SimpleNamespace(
            parameters=lambda instantiated: parameters,
            instantiate=lambda params: self.fail("powerset probability overrides must not reinstantiate the pipeline"),
        )
        apply = load_function("pyannote/pyannote_diarize.py", "apply_segmentation_thresholds", {"deepcopy": deepcopy})
        with contextlib.redirect_stdout(io.StringIO()) as output:
            apply(pipeline, onset=0.5, offset=0.363)
        self.assertEqual(parameters["segmentation"], {"min_duration_off": 0.0})
        self.assertEqual(parameters["clustering"]["threshold"], 0.7)
        self.assertIn("Skipping segmentation onset", output.getvalue())
        self.assertIn("Skipping segmentation offset", output.getvalue())

    def test_pyannote_failed_instantiation_never_runs_mutated_pipeline(self):
        original = {"segmentation": {"threshold": 0.5, "min_duration_off": 0.0}}
        called = []
        class Pipeline:
            @classmethod
            def from_pretrained(cls, model, **kwargs):
                return cls()
            def to(self, device):
                pass
            def parameters(self, instantiated):
                return original
            def instantiate(self, params):
                self.partial_state = params
                raise ValueError("invalid pipeline state")
            def __call__(self, *args, **kwargs):
                called.append(True)
                raise AssertionError("inference must not run after failed instantiation")

        def exit_with_code(code):
            raise SystemExit(code)

        namespace = {"deepcopy": deepcopy, "Pipeline": Pipeline, "sys": SimpleNamespace(exit=exit_with_code), "torch": SimpleNamespace(cuda=SimpleNamespace(is_available=lambda: False), device=lambda value: value)}
        for name in ("resolve_device", "apply_segmentation_thresholds", "diarize_audio"):
            load_function("pyannote/pyannote_diarize.py", name, namespace)
        with contextlib.redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as failure:
            namespace["diarize_audio"]("input.wav", "unused.json", device="cpu", segmentation_onset=0.8)
        self.assertEqual(failure.exception.code, 1)
        self.assertEqual(called, [])
        self.assertEqual(original["segmentation"]["threshold"], 0.5)

    def test_cpu_fence_precedes_ml_imports(self):
        for relative in ("nvidia/canary_transcribe.py", "nvidia/canary_qwen_transcribe.py", "nvidia/parakeet_transcribe.py", "nvidia/parakeet_transcribe_buffered.py", "nvidia/sortformer_diarize.py", "pyannote/pyannote_diarize.py"):
            source = (ROOT / relative).read_text()
            tree = ast.parse(source)
            fence = next(node for node in tree.body if isinstance(node, ast.If) and "CUDA_VISIBLE_DEVICES" in ast.get_source_segment(source, node))
            imports = [node for node in tree.body if isinstance(node, (ast.Import, ast.ImportFrom)) and any(name in ast.get_source_segment(source, node) for name in ("torch", "nemo", "librosa", "pyannote"))]
            self.assertTrue(all(fence.lineno < node.lineno for node in imports), relative)
            for argv, expected in ((["runner", "--device", "cpu"], ""), (["runner", "--device=cpu"], ""), (["runner", "--device", "auto"], "0")):
                environment = {"CUDA_VISIBLE_DEVICES": "0"}
                exec(compile(ast.Module(body=[fence], type_ignores=[]), relative, "exec"), {"sys": SimpleNamespace(argv=argv), "os": SimpleNamespace(environ=environment)})
                self.assertEqual(environment["CUDA_VISIBLE_DEVICES"], expected, relative)

    def test_explicit_cpu_wins_on_cuda_host(self):
        for relative in ("research/research_diarize.py", "whisperx/whisperx_run.py"):
            with self.subTest(runtime=relative):
                module = load_module(relative)
                self.assertEqual(module.resolve_device("cpu", True), "cpu")
                self.assertEqual(module.resolve_device("auto", True), "cuda")
                self.assertEqual(module.resolve_device("auto", False), "cpu")
                with self.assertRaisesRegex(RuntimeError, "CUDA was requested"):
                    module.resolve_device("cuda", False)

    def test_pyannote_explicit_cpu_wins_on_cuda_host(self):
        cuda = SimpleNamespace(is_available=lambda: True)
        resolve = load_function("pyannote/pyannote_diarize.py", "resolve_device", {"torch": SimpleNamespace(cuda=cuda)})
        self.assertEqual(resolve("cpu"), "cpu")
        self.assertEqual(resolve("auto"), "cuda")
        cuda.is_available = lambda: False
        with self.assertRaisesRegex(RuntimeError, "CUDA was requested"):
            resolve("cuda")

    def test_research_output_preserves_overlap(self):
        module = load_module("research/research_diarize.py")
        tracks = [(SimpleNamespace(start=1, end=3), 0, "B"), (SimpleNamespace(start=0, end=2), 1, "A")]
        annotation = SimpleNamespace(itertracks=lambda yield_label: iter(tracks))
        output = module.serialize_output(SimpleNamespace(speaker_diarization=annotation), "model", "cpu")
        self.assertEqual(output["speaker_count"], 2)
        self.assertEqual(output["segments"][0]["end"], 2)
        self.assertEqual(output["segments"][1]["start"], 1)
        self.assertEqual(output["resolved_device"], "cpu")
        self.assertNotIn("confidence", output["segments"][0])

    def test_canary_qwen_prompt_keeps_legacy_and_one_audio_marker(self):
        build = load_function("nvidia/canary_qwen_transcribe.py", "build_prompt", {"json": json})
        model = SimpleNamespace(audio_locator_tag="<audio>")
        self.assertEqual(build(model, "Legacy wording"), "Legacy wording <audio>")
        self.assertEqual(build(model, "Existing <audio>"), "Existing <audio>")
        prompt = build(model, "Legacy wording", 'Discuss Kubernetes <audio> "namespace"')
        self.assertIn("Legacy wording", prompt)
        self.assertIn("Kubernetes", prompt)
        self.assertEqual(prompt.count("<audio>"), 1)
        self.assertIn("Do not include these notes or invent speech", prompt)


if __name__ == "__main__":
    unittest.main()
