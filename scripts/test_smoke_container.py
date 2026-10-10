"""Readiness qualification must remain strict and keep diagnostics private."""

import argparse
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    "smoke_container", Path(__file__).with_name("smoke_container.py")
)
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class ImageReadinessTests(unittest.TestCase):
    def test_ready_endpoint_alone_does_not_qualify_an_unhealthy_image(self):
        state = {
            "Running": True,
            "Health": {
                "Status": "unhealthy",
                "FailingStreak": 3,
                "Log": [{"ExitCode": 7, "Output": "private-health-output"}],
            },
        }
        response = json.dumps({
            "status": "ready",
            "checks": {"database": "ready", "queue": "ready"},
            "private": "private-response",
        }).encode()
        with (
            patch.object(smoke, "docker", return_value=json.dumps(state)),
            patch.object(smoke, "request", return_value=response),
            patch.object(smoke.time, "monotonic", side_effect=[0, 0, 2]),
            patch.object(smoke.time, "sleep"),
        ):
            with self.assertRaises(TimeoutError) as result:
                smoke.wait_ready("owned-smoke", "http://127.0.0.1:12345", 1)
        message = str(result.exception)
        self.assertIn('"docker_health": "unhealthy"', message)
        self.assertIn('"last_health_exit_code": 7', message)
        self.assertIn('"ready_status": "ready"', message)
        self.assertNotIn("private-health-output", message)
        self.assertNotIn("private-response", message)

    def test_healthy_image_still_requires_successful_readiness(self):
        state = {"Running": True, "Health": {"Status": "healthy"}}
        with (
            patch.object(smoke, "docker", return_value=json.dumps(state)),
            patch.object(
                smoke, "request",
                side_effect=AssertionError("/ready: expected HTTP 200, got 503"),
            ),
            patch.object(smoke.time, "monotonic", side_effect=[0, 0, 2]),
            patch.object(smoke.time, "sleep"),
        ):
            with self.assertRaisesRegex(TimeoutError, "HTTP 200, got 503"):
                smoke.wait_ready("owned-smoke", "http://127.0.0.1:12345", 1)

    def test_missing_healthcheck_is_a_failure_with_numeric_diagnostics(self):
        with (
            patch.object(smoke, "docker", return_value=json.dumps({"Running": True})),
            patch.object(smoke, "request", return_value=b'{"status":"ready"}'),
            patch.object(smoke.time, "monotonic", side_effect=[0, 0, 2]),
            patch.object(smoke.time, "sleep"),
        ):
            with self.assertRaisesRegex(TimeoutError, '"docker_health": "missing"'):
                smoke.wait_ready("owned-smoke", "http://127.0.0.1:12345", 1)

    def test_ready_and_healthy_image_passes(self):
        state = {"Running": True, "Health": {"Status": "healthy"}}
        with (
            patch.object(smoke, "docker", return_value=json.dumps(state)),
            patch.object(
                smoke, "request",
                return_value=b'{"status":"ready","checks":{"database":"ready","queue":"ready"}}',
            ),
            patch.object(smoke.time, "monotonic", side_effect=[0, 0]),
        ):
            smoke.wait_ready("owned-smoke", "http://127.0.0.1:12345", 1)


class ImageRestartTests(unittest.TestCase):
    def test_restart_reconnects_to_current_port_and_preserves_token(self):
        current_port = 0
        restarts = 0
        authenticated_after_restart = 0

        def docker(*args):
            nonlocal current_port, restarts
            if args[0] == "run":
                current_port = 32000 + restarts * 2
            elif args[0] == "restart":
                current_port += 1
                restarts += 1
            elif args[0] == "inspect":
                return json.dumps({"8080/tcp": [{"HostIp": "127.0.0.1", "HostPort": str(current_port)}]})
            elif args[0] == "exec" and args[2] == "awk":
                uid, gid = (99, 100) if args[1].endswith("-unraid") else (1000, 1000)
                return f"Uid: {uid}\nGid: {gid}"
            return ""

        def readiness(name, base, timeout):
            self.assertEqual(base, f"http://127.0.0.1:{current_port}")

        def request(base, path, **kwargs):
            nonlocal authenticated_after_restart
            self.assertEqual(base, f"http://127.0.0.1:{current_port}")
            if path == "/health":
                return b'{"version":"ci","commit":"fixture-revision"}'
            if path.endswith("/auth/login"):
                return b'{"token":"fixture-jwt"}'
            if path.endswith("/transcription/upload"):
                return b'{"id":"fixture-recording"}'
            if path.endswith("/fixture-recording"):
                self.assertEqual(kwargs.get("token"), "fixture-jwt")
                if current_port % 2:
                    authenticated_after_restart += 1
                return b'{"title":"Container smoke recording"}'
            if path.endswith("/audio"):
                return b"RIFF"
            if path.endswith("/settings/models"):
                return b'{"entries":[]}'
            if path == "/install.sh":
                return b"#!/bin/sh"
            if path.startswith("/api/v1/cli/download"):
                return b"\x7fELF"
            return b"{}"

        args = argparse.Namespace(image="fixture-image", revision="fixture-revision", version="ci", timeout=1)
        with (
            patch.object(smoke, "docker", side_effect=docker),
            patch.object(smoke, "request", side_effect=request),
            patch.object(smoke, "wait_ready", side_effect=readiness),
            patch.object(smoke, "qualify_runtime_tools", return_value={"native_python_extension": True}),
            patch.object(smoke, "qualify_kernel_headers", return_value={"regular_headers_and_docs_only": True}),
            patch.object(smoke.subprocess, "run") as process,
        ):
            process.return_value.returncode = 0
            result = smoke.qualify(args)
        self.assertTrue(result["passed"])
        self.assertEqual(restarts, 2)
        self.assertEqual(authenticated_after_restart, 2)
        self.assertEqual([case["identity"] for case in result["cases"]], ["default", "unraid"])

    def test_invalid_bindings_fail_without_printing_inspect_data(self):
        for bindings in (None, [], [{"HostIp": "0.0.0.0", "HostPort": "12345"}],
                         [{"HostIp": "127.0.0.1", "HostPort": ""}],
                         [{"HostIp": "127.0.0.1", "HostPort": "private-invalid-port"}],
                         [{"HostIp": "127.0.0.1", "HostPort": "65536"}]):
            with self.subTest(bindings=bindings), patch.object(
                smoke, "docker", return_value=json.dumps({"8080/tcp": bindings, "private": "private-inspect-data"})
            ):
                with self.assertRaises(AssertionError) as result:
                    smoke.container_base_url("owned-smoke")
                self.assertNotIn("private", str(result.exception))


if __name__ == "__main__":
    unittest.main()
