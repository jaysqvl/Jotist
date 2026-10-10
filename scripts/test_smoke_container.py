"""Readiness qualification must remain strict and keep diagnostics private."""

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


if __name__ == "__main__":
    unittest.main()
