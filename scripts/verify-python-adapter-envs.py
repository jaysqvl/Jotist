#!/usr/bin/env python3
from __future__ import annotations

import argparse
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import tomllib
from dataclasses import dataclass
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SECURITY_DIRECT_PINS = {"lightning", "pytorch-lightning", "hydra-core", "nltk"}


@dataclass(frozen=True)
class PackageRange:
    name: str
    min_inclusive: str | None = None
    max_exclusive: str | None = None
    exact: str | None = None


@dataclass(frozen=True)
class SourceExpectation:
    package: str
    field: str
    value: str


@dataclass(frozen=True)
class AdapterSpec:
    key: str
    label: str
    pyproject: Path
    import_code: str
    requires_python: str
    package_ranges: tuple[PackageRange, ...] = ()
    expected_sources: tuple[SourceExpectation, ...] = ()
    bootstrap_script: str | None = None
    caller_scripts: tuple[str, ...] = ()
    expected_overrides: tuple[str, ...] = ()
    torch_companions: tuple[str, ...] = ("torchaudio", "torchcodec")
    pair_import_packages: tuple[str, ...] = ()
    pair_import_code: str | None = None


ADAPTERS: tuple[AdapterSpec, ...] = (
    AdapterSpec(
        key="nvidia-asr",
        label="NVIDIA ASR adapters",
        pyproject=ROOT / "internal/transcription/adapters/py/nvidia/pyproject.toml",
        import_code="import nemo.collections.asr",
        requires_python=">=3.11,<3.13",
        package_ranges=(
            PackageRange("torch", exact="2.14.0"),
            PackageRange("torchaudio", exact="2.11.0"),
            PackageRange("nemo-toolkit", exact="3.0.0"),
            PackageRange("lightning", exact="2.6.6"),
            PackageRange("pytorch-lightning", exact="2.6.6"),
            PackageRange("hydra-core", exact="1.3.6"),
            PackageRange("nv-one-logger-pytorch-lightning-integration", exact="2.3.1+jotist.1"),
            PackageRange("transformers", exact="5.17.0"),
            PackageRange("torchcodec", exact="0.16.0"),
            PackageRange("huggingface-hub", exact="1.31.0"),
            PackageRange("ml-dtypes", exact="0.6.0"),
            PackageRange("onnx", exact="1.22.0"),
        ),
        expected_sources=(SourceExpectation("nv-one-logger-pytorch-lightning-integration", "path", "vendor/nv-one-logger-pytorch-lightning-integration"),),
        bootstrap_script="nvidia_compat.py",
        expected_overrides=("lightning==2.6.6", "hydra-core==1.3.6"),
        pair_import_packages=("onnx", "ml-dtypes"),
        pair_import_code=(
            "import ml_dtypes, onnx; "
            "print(f'onnx={onnx.__version__} ml_dtypes={ml_dtypes.__version__}')"
        ),
    ),
    AdapterSpec(
        key="canary-qwen",
        label="Canary-Qwen SALM adapter",
        pyproject=ROOT / "internal/transcription/adapters/py/nvidia/canary_qwen_pyproject.toml",
        import_code="from nemo.collections.speechlm2.models import SALM",
        requires_python=">=3.11,<3.13",
        package_ranges=(
            PackageRange("torch", exact="2.14.0"),
            PackageRange("torchaudio", exact="2.11.0"),
            PackageRange("nemo-toolkit", exact="3.0.0"),
            PackageRange("lightning", exact="2.6.6"),
            PackageRange("pytorch-lightning", exact="2.6.6"),
            PackageRange("hydra-core", exact="1.3.6"),
            PackageRange("nltk", exact="3.10.3"),
            PackageRange("nv-one-logger-pytorch-lightning-integration", exact="2.3.1+jotist.1"),
            PackageRange("transformers", exact="5.17.0"),
            PackageRange("torchcodec", exact="0.16.0"),
            PackageRange("huggingface-hub", exact="1.31.0"),
            PackageRange("ml-dtypes", exact="0.6.0"),
            PackageRange("onnx", exact="1.22.0"),
        ),
        expected_sources=(SourceExpectation("nv-one-logger-pytorch-lightning-integration", "path", "vendor/nv-one-logger-pytorch-lightning-integration"),),
        bootstrap_script="nvidia_compat.py",
        caller_scripts=("canary_qwen_transcribe.py",),
        expected_overrides=("lightning==2.6.6", "hydra-core==1.3.6"),
        pair_import_packages=("onnx", "ml-dtypes"),
        pair_import_code=(
            "import ml_dtypes, onnx; "
            "assert hasattr(ml_dtypes, 'float4_e2m1fn'), "
            "'ml_dtypes.float4_e2m1fn is missing'; "
            "print(f'onnx={onnx.__version__} ml_dtypes={ml_dtypes.__version__}')"
        ),
    ),
    AdapterSpec(
        key="pyannote",
        label="Pyannote diarization adapter",
        pyproject=ROOT / "internal/transcription/adapters/py/pyannote/pyproject.toml",
        import_code="from pyannote.audio import Pipeline",
        requires_python=">=3.10,<3.13",
        package_ranges=(
            PackageRange("pyannote.audio", exact="4.0.7"),
            PackageRange("lightning", exact="2.6.6"),
            PackageRange("pytorch-lightning", exact="2.6.6"),
            PackageRange("torch", exact="2.14.0"),
            PackageRange("torchaudio", exact="2.11.0"),
            PackageRange("torchcodec", exact="0.16.0"),
            PackageRange("huggingface-hub", exact="1.31.0"),
        ),
    ),
    AdapterSpec(
        key="whisperx", label="WhisperX", pyproject=ROOT / "internal/transcription/adapters/py/whisperx/pyproject.toml",
        import_code=(
            "from whisperx.alignment import load_align_model; "
            "from whisperx.asr import load_model; "
            "from whisperx.transcribe import transcribe_task; "
            "from whisperx.diarize import DiarizationPipeline"
        ), requires_python=">=3.11,<3.13",
        caller_scripts=("whisperx_run.py",),
        package_ranges=(PackageRange("whisperx", exact="3.8.7rc1+jotist.1"), PackageRange("torch", exact="2.14.0"), PackageRange("torchaudio", exact="2.11.0"), PackageRange("torchvision", exact="0.29.0"), PackageRange("torchcodec", exact="0.16.0"), PackageRange("transformers", exact="5.17.0"), PackageRange("huggingface-hub", exact="1.31.0"), PackageRange("lightning", exact="2.6.6"), PackageRange("pytorch-lightning", exact="2.6.6"), PackageRange("nltk", exact="3.10.3")),
        torch_companions=("torchaudio", "torchvision", "torchcodec"),
    ),
    AdapterSpec(
        key="suplime", label="SUPlime research diarization", pyproject=ROOT / "internal/transcription/adapters/py/suplime/pyproject.toml",
        import_code="import suplime; from pyannote.audio import Pipeline", requires_python=">=3.11,<3.13",
        package_ranges=(PackageRange("suplime", exact="0.2.0"), PackageRange("pyannote.audio", exact="4.0.7"), PackageRange("torch", exact="2.14.0"), PackageRange("torchaudio", exact="2.11.0"), PackageRange("torchcodec", exact="0.16.0"), PackageRange("huggingface-hub", exact="1.31.0"), PackageRange("lightning", exact="2.6.6"), PackageRange("pytorch-lightning", exact="2.6.6")),
    ),
    AdapterSpec(
        key="diarizen", label="DiariZen research diarization", pyproject=ROOT / "internal/transcription/adapters/py/diarizen/pyproject.toml",
        import_code="from diarizen.pipelines.inference import DiariZenPipeline", requires_python=">=3.12,<3.13",
        package_ranges=(PackageRange("diarizen", exact="0.0.1+jotist.1"), PackageRange("pyannote.audio", exact="3.1.1+jotist.1"), PackageRange("torch", exact="2.14.0"), PackageRange("torchaudio", exact="2.11.0"), PackageRange("torchcodec", exact="0.16.0"), PackageRange("transformers", exact="5.17.0"), PackageRange("huggingface-hub", exact="1.31.0"), PackageRange("accelerate", exact="1.15.0"), PackageRange("lightning", exact="2.6.6"), PackageRange("pytorch-lightning", exact="2.6.6")),
        expected_sources=(SourceExpectation("diarizen", "path", "vendor/diarizen"), SourceExpectation("pyannote.audio", "path", "vendor/pyannote-audio")),
    ),
    AdapterSpec(
        key="local-asr", label="Modern local ASR", pyproject=ROOT / "internal/transcription/adapters/py/local_asr/pyproject.toml",
        import_code="import torch, torchaudio; from transformers import AutoProcessor, AutoModelForSpeechSeq2Seq", requires_python=">=3.11,<3.13",
        package_ranges=(PackageRange("transformers", exact="5.17.0"), PackageRange("torch", exact="2.14.0"), PackageRange("torchaudio", exact="2.11.0"), PackageRange("torchcodec", exact="0.16.0"), PackageRange("huggingface-hub", exact="1.31.0"), PackageRange("accelerate", exact="1.15.0")),
    ),
)


# Importing pyannote alone can merely warn about a broken TorchCodec wheel.
# Exercise the native decoder on an in-memory generated public-domain signal.
AUDIO_IMPORT_CHECK = """
import tempfile, wave
from pathlib import Path
import torch, torchaudio, torchcodec
from torchcodec.decoders import AudioDecoder
with tempfile.TemporaryDirectory() as audio_dir:
    audio_path = Path(audio_dir) / "silence.wav"
    with wave.open(str(audio_path), "wb") as wav:
        wav.setnchannels(1)
        wav.setsampwidth(2)
        wav.setframerate(16000)
        wav.writeframes(b"\\x00\\x00" * 1600)
    decoded = AudioDecoder(str(audio_path)).get_all_samples()
    assert decoded.sample_rate == 16000 and decoded.data.shape == (1, 1600)
    samples, rate = torchaudio.load(str(audio_path))
    assert rate == 16000 and samples.shape == (1, 1600)
print(f"native audio: torch={torch.__version__} torchaudio={torchaudio.__version__} torchcodec={torchcodec.__version__}")
"""


ADAPTERS_BY_KEY = {adapter.key: adapter for adapter in ADAPTERS}


class CheckError(Exception):
    pass


def canonical_name(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name).lower()


def version_parts(version: str) -> tuple[int, ...]:
    match = re.match(r"^(\d+(?:\.\d+)*)", version)
    if match is None:
        raise CheckError(f"Cannot compare non-numeric version {version!r}")
    return tuple(int(part) for part in match.group(1).split("."))


def compare_versions(left: str, right: str) -> int:
    left_parts = version_parts(left)
    right_parts = version_parts(right)
    length = max(len(left_parts), len(right_parts))
    left_padded = left_parts + (0,) * (length - len(left_parts))
    right_padded = right_parts + (0,) * (length - len(right_parts))
    return (left_padded > right_padded) - (left_padded < right_padded)


def run(
    args: list[str],
    cwd: Path,
    timeout: int,
    verbose: bool,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    if verbose:
        print(f"+ {shlex.join(args)}", flush=True)
    try:
        completed = subprocess.run(
            args,
            cwd=cwd,
            env=env,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired as exc:
        raise CheckError(f"Timed out after {timeout}s: {shlex.join(args)}") from exc

    if verbose and completed.stdout:
        print(completed.stdout, end="")
    if verbose and completed.stderr:
        print(completed.stderr, end="", file=sys.stderr)

    if completed.returncode != 0:
        output = "\n".join(part for part in (completed.stdout, completed.stderr) if part)
        raise CheckError(
            f"Command failed with exit code {completed.returncode}: {shlex.join(args)}\n"
            f"{output.strip()}"
        )
    return completed


def load_toml(path: Path) -> dict:
    return tomllib.loads(path.read_text())


def validate_pyproject(spec: AdapterSpec) -> None:
    pyproject = load_toml(spec.pyproject)
    project = pyproject.get("project", {})
    requires_python = project.get("requires-python")
    if requires_python != spec.requires_python:
        raise CheckError(
            f"{spec.pyproject} has requires-python={requires_python!r}; "
            f"expected {spec.requires_python!r}"
        )

    # A clean resolver may choose the fixed release even after a direct pin is
    # removed, while an existing lockfile retains an older transitive version.
    # Require policy-sensitive versions in the recipe as well as the lockfile.
    direct_pins = {}
    for dependency in project.get("dependencies", []):
        match = re.fullmatch(r"\s*([A-Za-z0-9_.-]+)\s*==\s*([^\s;]+)\s*", dependency)
        if match:
            direct_pins[canonical_name(match.group(1))] = match.group(2)
    for expected in spec.package_ranges:
        name = canonical_name(expected.name)
        if name in SECURITY_DIRECT_PINS and direct_pins.get(name) != expected.exact:
            raise CheckError(f"{spec.key}: {name}=={expected.exact} must be a direct dependency pin")

    uv_settings = pyproject.get("tool", {}).get("uv", {})
    if sorted(uv_settings.get("override-dependencies", [])) != sorted(spec.expected_overrides):
        raise CheckError(f"{spec.key}: dependency overrides differ from the reviewed compatibility policy")
    sources = uv_settings.get("sources", {})
    for companion in spec.torch_companions:
        # Exact public versions alone allow a PyPI CUDA vision wheel beside a
        # +cpu Torch wheel. Every platform must use the same selected index.
        if not sources.get("torch") or sources.get(companion) != sources["torch"]:
            raise CheckError(
                f"{spec.key}: {companion} must use the same platform-specific wheel sources as torch"
            )
    for expected in spec.expected_sources:
        source = sources.get(expected.package)
        if not isinstance(source, dict):
            raise CheckError(f"{expected.package} source is not pinned in {spec.pyproject}")
        actual = source.get(expected.field)
        if actual != expected.value:
            raise CheckError(
                f"{expected.package} {expected.field}={actual!r}; expected {expected.value!r}"
            )


def copy_pyproject(spec: AdapterSpec, temp_root: Path, torch_index: str) -> Path:
    workdir = temp_root / spec.key
    workdir.mkdir(parents=True, exist_ok=True)
    # Local compatibility distributions are part of the runtime, including
    # package files such as __init__.py. Resolve the actual shipped source.
    vendor = spec.pyproject.parent / "vendor"
    if vendor.is_dir():
        shutil.copytree(vendor, workdir / "vendor", dirs_exist_ok=True,
                        ignore=shutil.ignore_patterns(".git", ".venv", "__pycache__", "*.pyc"))
    if spec.bootstrap_script:
        shutil.copy2(spec.pyproject.parent / spec.bootstrap_script, workdir / spec.bootstrap_script)
    for caller in spec.caller_scripts:
        shutil.copy2(spec.pyproject.parent / caller, workdir / caller)
    pyproject_content = spec.pyproject.read_text()
    backend = ("cpu" if spec.key == "local-asr" else "cu126") if torch_index == "project" else torch_index
    pyproject_content = pyproject_content.replace(
        'url = "https://download.pytorch.org/whl/cpu"' if spec.key == "local-asr" else 'url = "https://download.pytorch.org/whl/cu126"',
        f'url = "https://download.pytorch.org/whl/{backend}"', 1,
    )
    if spec.key in ("nvidia-asr", "canary-qwen") and backend == "cu130":
        pyproject_content = pyproject_content.replace("numba-cuda[cu12]", "numba-cuda[cu13]")
        pyproject_content = pyproject_content.replace("cuda-python>=12,<13", "cuda-python>=13,<14")
    (workdir / "pyproject.toml").write_text(pyproject_content)
    return workdir


def lock_environment(
    spec: AdapterSpec,
    workdir: Path,
    uv: str,
    python_version: str,
    timeout: int,
    verbose: bool,
) -> dict[str, list[str]]:
    run(
        [
            uv,
            "lock",
            "--project",
            str(workdir),
            "--python",
            python_version,
            "--system-certs",
        ],
        cwd=ROOT,
        timeout=timeout,
        verbose=verbose,
    )

    lock_path = workdir / "uv.lock"
    if not lock_path.exists():
        raise CheckError(f"uv did not write {lock_path}")

    lock_data = load_toml(lock_path)
    packages: dict[str, list[str]] = {}
    for package in lock_data.get("package", []):
        name = package.get("name")
        version = package.get("version")
        if name and version:
            packages.setdefault(canonical_name(name), []).append(version)

    check_package_ranges(spec, packages)
    return packages


def check_package_ranges(spec: AdapterSpec, packages: dict[str, list[str]]) -> None:
    for package_range in spec.package_ranges:
        key = canonical_name(package_range.name)
        versions = packages.get(key)
        if not versions:
            raise CheckError(f"{spec.key}: {package_range.name} was not present in uv.lock")

        for version in versions:
            # PEP 440 public versions also select their +cpu / +cu126 wheels.
            expected = package_range.exact
            compared = version if expected and "+" in expected else version.split("+", 1)[0]
            if expected is not None and compared != expected:
                raise CheckError(
                    f"{spec.key}: {package_range.name} resolved to {version}; "
                    f"expected {package_range.exact}"
                )
            if (
                package_range.min_inclusive is not None
                and compare_versions(version, package_range.min_inclusive) < 0
            ):
                raise CheckError(
                    f"{spec.key}: {package_range.name} resolved to {version}; "
                    f"expected >= {package_range.min_inclusive}"
                )
            if (
                package_range.max_exclusive is not None
                and compare_versions(version, package_range.max_exclusive) >= 0
            ):
                raise CheckError(
                    f"{spec.key}: {package_range.name} resolved to {version}; "
                    f"expected < {package_range.max_exclusive}"
                )

        unique_versions = ", ".join(sorted(set(versions)))
        print(f"    {package_range.name}: {unique_versions}")


def run_pair_import_check(
    spec: AdapterSpec,
    packages: dict[str, list[str]],
    uv: str,
    python_version: str,
    timeout: int,
    verbose: bool,
) -> None:
    if not spec.pair_import_packages or spec.pair_import_code is None:
        return

    cmd = [
        uv,
        "run",
        "--no-project",
        "--python",
        python_version,
        "--system-certs",
    ]
    for package in spec.pair_import_packages:
        versions = packages.get(canonical_name(package))
        if not versions:
            raise CheckError(f"{spec.key}: cannot import-check missing package {package}")
        if len(set(versions)) != 1:
            raise CheckError(f"{spec.key}: package {package} resolved multiple versions: {versions}")
        cmd.extend(["--with", f"{package}=={versions[0]}"])
    cmd.extend(["python", "-c", spec.pair_import_code])

    completed = run(cmd, cwd=ROOT, timeout=timeout, verbose=verbose)
    output = completed.stdout.strip()
    if output:
        print(f"    pair import: {output}")
    else:
        print("    pair import: ok")


def run_adapter_import_check(
    spec: AdapterSpec,
    workdir: Path,
    uv: str,
    python_version: str,
    timeout: int,
    verbose: bool,
) -> None:
    print(f"    import: {spec.import_code.splitlines()[-1]}")
    import_code = spec.import_code
    if spec.bootstrap_script:
        bootstrap = str(workdir / spec.bootstrap_script)
        import_code = f"import runpy; runpy.run_path({bootstrap!r}); " + import_code
    run(
        [
            uv,
            "run",
            "--project",
            str(workdir),
            "--python",
            python_version,
            "--locked",
            "--system-certs",
            "python",
            "-I",
            "-c",
            import_code + "\n" + AUDIO_IMPORT_CHECK,
        ],
        cwd=ROOT,
        timeout=timeout,
        verbose=verbose,
    )


def run_adapter_audit(spec, workdir, args):
    command = [args.uv, "run", "--no-sync", "--project", str(workdir),
               "python", "-I", str(ROOT / "scripts/audit-python-adapter-env.py"),
               "--adapter", spec.key, "--caller-root", str(workdir)]
    if args.audit_output_dir:
        command += ["--output", str(args.audit_output_dir.resolve() / f"{spec.key}.json")]
    completed = run(command, cwd=ROOT, timeout=args.timeout, verbose=args.verbose)
    print(completed.stdout.strip())


def selected_adapters(keys: list[str] | None) -> list[AdapterSpec]:
    if not keys:
        return list(ADAPTERS)
    return [ADAPTERS_BY_KEY[key] for key in keys]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Validate Jotist Python adapter resolver output and optional imports."
    )
    parser.add_argument(
        "--adapter",
        action="append",
        choices=sorted(ADAPTERS_BY_KEY),
        help="Adapter to check. Repeat to check multiple adapters. Defaults to all.",
    )
    parser.add_argument(
        "--mode",
        choices=("lock", "import"),
        default="lock",
        help="lock checks resolver output; import also materializes the adapter env and imports it.",
    )
    parser.add_argument(
        "--python",
        default="3.12",
        help="Python version uv should resolve against, for example 3.11 or 3.12.",
    )
    parser.add_argument("--uv", default="uv", help="uv executable to run.")
    parser.add_argument("--audit", action="store_true",
                        help="In import mode, audit the installed graph against OSV and the scoped source policy.")
    parser.add_argument("--audit-output-dir", type=Path,
                        help="Save one installed inventory/advisory JSON report per adapter.")
    parser.add_argument(
        "--torch-index",
        choices=("project", "cpu", "cu126", "cu130"),
        default="project",
        help="Select a supported wheel backend; project preserves each adapter default.",
    )
    parser.add_argument("--timeout", type=int, default=1800, help="Per-command timeout in seconds.")
    parser.add_argument("--verbose", action="store_true", help="Print full uv command output.")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.audit and args.mode != "import":
        print("--audit requires --mode import so it checks installed distributions", file=sys.stderr)
        return 2
    adapters = selected_adapters(args.adapter)
    failures: list[str] = []

    with tempfile.TemporaryDirectory(prefix="jotist-adapter-envs-") as temp_dir:
        temp_root = Path(temp_dir)
        for spec in adapters:
            print(f"==> {spec.key} ({spec.label}) on Python {args.python}", flush=True)
            try:
                validate_pyproject(spec)
                workdir = copy_pyproject(spec, temp_root, args.torch_index)
                packages = lock_environment(
                    spec,
                    workdir,
                    args.uv,
                    args.python,
                    args.timeout,
                    args.verbose,
                )
                run_pair_import_check(
                    spec,
                    packages,
                    args.uv,
                    args.python,
                    args.timeout,
                    args.verbose,
                )
                if args.mode == "import":
                    run_adapter_import_check(
                        spec,
                        workdir,
                        args.uv,
                        args.python,
                        args.timeout,
                        args.verbose,
                    )
                    if args.audit:
                        run_adapter_audit(spec, workdir, args)
            except CheckError as exc:
                failures.append(f"{spec.key}: {exc}")
                print(f"    FAILED: {exc}", file=sys.stderr)

    if failures:
        print("\nPython adapter environment checks failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    print("\nPython adapter environment checks passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
