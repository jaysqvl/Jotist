#!/usr/bin/env python3
"""Boot the actual image using only disposable, task-owned Docker resources.

This qualifies packaging, authentication, mounted writes, and restart persistence.
It does not claim that a CPU runner has qualified CUDA inference.
"""
from __future__ import annotations

import argparse
import io
import json
import secrets
import subprocess
import time
import urllib.error
import urllib.request
import uuid
import wave
from pathlib import Path


def docker(*args: str) -> str:
    result = subprocess.run(["docker", *args], capture_output=True, text=True, check=False)
    if result.returncode:
        # Never dump container logs, environment, or authenticated responses.
        raise RuntimeError(f"docker {args[0]} failed (exit {result.returncode})")
    return result.stdout.strip()


def request(base: str, path: str, *, token: str = "", body: bytes | None = None,
            content_type: str = "application/json", status: int = 200) -> bytes:
    headers = {"Content-Type": content_type}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    req = urllib.request.Request(base + path, data=body, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            code, data = response.status, response.read()
    except urllib.error.HTTPError as error:
        code, data = error.code, error.read()
    if code != status:
        raise AssertionError(f"{path}: expected HTTP {status}, got {code}")
    return data


def wait_ready(name: str, base: str, timeout: int) -> None:
    deadline = time.monotonic() + timeout
    diagnostic: dict = {}
    while time.monotonic() < deadline:
        state = json.loads(docker("inspect", "--format", "{{json .State}}", name))
        if not state.get("Running"):
            raise AssertionError(f"container exited (exit {state.get('ExitCode')})")
        health = state.get("Health", {})
        health_status = health.get("Status", "missing")
        diagnostic = {
            "docker_health": (
                health_status
                if health_status in ("healthy", "unhealthy", "starting", "missing")
                else "unknown"
            ),
            "health_failures": health.get("FailingStreak", 0),
        }
        # Healthcheck output and container logs can contain private data. Keep
        # only the exit code, never Output, environment or response bodies.
        if health.get("Log"):
            diagnostic["last_health_exit_code"] = health["Log"][-1].get("ExitCode")
        try:
            data = json.loads(request(base, "/ready"))
            ready_status = data.get("status")
            diagnostic["ready_status"] = (
                ready_status if ready_status in ("ready", "not_ready") else "unknown"
            )
            checks = data.get("checks", {})
            for component in ("database", "queue"):
                value = checks.get(component)
                diagnostic[component] = value if value in ("ready", "unavailable") else "unknown"
            if ready_status == "ready" and health_status == "healthy":
                return
        except AssertionError as error:
            # request() constructs this message from the known path and HTTP
            # status only; it does not include an authenticated response body.
            diagnostic["readiness_http_result"] = str(error)
        except (OSError, ValueError) as error:
            diagnostic["readiness_probe_error"] = type(error).__name__
        time.sleep(2)
    raise TimeoutError(
        "built image did not become ready and Docker healthy: "
        + json.dumps(diagnostic, sort_keys=True)
    )


def multipart_audio() -> tuple[bytes, str]:
    audio = io.BytesIO()
    with wave.open(audio, "wb") as output:
        output.setparams((1, 2, 16000, 0, "NONE", "not compressed"))
        output.writeframes(b"\x00\x00" * 16000)
    boundary = "jotist-" + uuid.uuid4().hex
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\n"
            "Container smoke recording\r\n"
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"audio\"; filename=\"smoke.wav\"\r\n"
            "Content-Type: audio/wav\r\n\r\n").encode()
    return body + audio.getvalue() + f"\r\n--{boundary}--\r\n".encode(), f"multipart/form-data; boundary={boundary}"


def qualify(args: argparse.Namespace) -> dict:
    prefix = "jotist-smoke-" + uuid.uuid4().hex
    volumes = [prefix + "-" + suffix for suffix in ("data", "runtime", "home")]
    created_volumes: list[str] = []
    containers: list[str] = []
    report = {"image": args.image, "revision": args.revision, "version": args.version,
              "kind": "container-packaging", "cuda_inference_qualified": False, "cases": []}
    started = time.monotonic()
    password = secrets.token_urlsafe(24)
    credentials = {"username": "smoke-owner", "password": password, "confirmPassword": password}
    recording_id = ""
    try:
        for volume in volumes:
            docker("volume", "create", "--label", f"jotist.smoke={prefix}", volume)
            created_volumes.append(volume)
        for label, uid, gid in (("default", "1000", "1000"), ("unraid", "99", "100")):
            name = prefix + "-" + label
            containers.append(name)
            docker("run", "--detach", "--name", name, "--label", f"jotist.smoke={prefix}",
                   "--publish", "127.0.0.1::8080", "--env", f"PUID={uid}", "--env", f"PGID={gid}",
                   "--env", "LOG_LEVEL=error", "--env", "NVIDIA_VISIBLE_DEVICES=void",
                   "--mount", f"type=volume,src={volumes[0]},dst=/app/data",
                   "--mount", f"type=volume,src={volumes[1]},dst=/app/whisperx-env",
                   "--mount", f"type=volume,src={volumes[2]},dst=/home/appuser", args.image)
            port = json.loads(docker("inspect", "--format", "{{json .NetworkSettings.Ports}}", name))["8080/tcp"][0]["HostPort"]
            base = f"http://127.0.0.1:{port}"
            wait_ready(name, base, args.timeout)
            health = json.loads(request(base, "/health"))
            assert health.get("version") == args.version, "runtime release version differs"
            assert health.get("commit") == args.revision, "runtime source revision differs"
            identity = docker("exec", name, "awk", "/^Uid:|^Gid:/ { print $1, $2 }", "/proc/1/status").splitlines()
            assert identity == [f"Uid: {uid}", f"Gid: {gid}"], "runtime UID/GID differs from request"
            request(base, "/api/v1/settings/models", status=401)
            if not recording_id:
                request(base, "/api/v1/auth/register", body=json.dumps(credentials).encode(), status=201)
            login = json.loads(request(base, "/api/v1/auth/login", body=json.dumps(credentials).encode()))
            token = login["token"]
            if not recording_id:
                body, content_type = multipart_audio()
                uploaded = json.loads(request(base, "/api/v1/transcription/upload", token=token, body=body, content_type=content_type))
                recording_id = uploaded["id"]
            recording = json.loads(request(base, f"/api/v1/transcription/{recording_id}", token=token))
            assert recording["title"] == "Container smoke recording", "recording did not persist"
            assert request(base, f"/api/v1/transcription/{recording_id}/audio", token=token)[:4] == b"RIFF", "audio did not persist"
            inventory = json.loads(request(base, "/api/v1/settings/models", token=token))
            assert isinstance(inventory["entries"], list), "storage inventory unavailable"
            assert request(base, "/install.sh").startswith(b"#!/"), "CLI installer unavailable"
            binary = request(base, "/api/v1/cli/download?os=linux&arch=amd64")
            assert binary[:4] == b"\x7fELF", "packaged Linux CLI is not an ELF executable"
            docker("exec", name, "gosu", "appuser", "/app/bin/cli/jotist-linux-amd64", "--help")
            # The same files must survive an actual stop/start, including the signing secret.
            docker("restart", "--time", "40", name)
            wait_ready(name, base, args.timeout)
            request(base, f"/api/v1/transcription/{recording_id}", token=token)
            report["cases"].append({"identity": label, "uid": int(uid), "gid": int(gid),
                                    "ready": True, "docker_healthy": True, "authenticated_upload": True,
                                    "restart_persistence": True, "cli": True})
            docker("rm", "--force", name)
            containers.remove(name)
        report["passed"] = True
        report["elapsed_seconds"] = round(time.monotonic() - started, 3)
        return report
    finally:
        cleanup_failures = 0
        for name in containers:
            # A failed run may have reserved a name without creating the container.
            exists = subprocess.run(["docker", "container", "ls", "--all", "--quiet", "--filter", f"name=^/{name}$"], capture_output=True, text=True, check=False)
            if exists.returncode:
                cleanup_failures += 1
            elif exists.stdout.strip():
                cleanup_failures += bool(subprocess.run(["docker", "rm", "--force", name], capture_output=True, check=False).returncode)
        for volume in created_volumes:
            cleanup_failures += bool(subprocess.run(["docker", "volume", "rm", volume], capture_output=True, check=False).returncode)
        if cleanup_failures:
            raise RuntimeError(f"disposable smoke resources could not be cleaned up ({cleanup_failures} operations)")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--timeout", type=int, default=900)
    parser.add_argument("--report", type=Path, required=True)
    arguments = parser.parse_args()
    result = qualify(arguments)
    arguments.report.parent.mkdir(parents=True, exist_ok=True)
    arguments.report.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result))
