"""Safe GPU execution evidence. No ML imports or user data in failure records."""
from contextlib import contextmanager
import json
import os
import time

_completed_units = 0


class RecoveryBudget:
    """One stage budget shared by decoder retries, window changes and repairs.

    The coordinator sends this through the worker environment. An absent value
    preserves immutable legacy plans. Malformed values fail closed.
    """

    def __init__(self, policy=None):
        self.policy = policy
        self.used = 0
        if policy is None:
            return
        if not isinstance(policy, dict) or type(policy.get("version")) is not int or policy["version"] != 1:
            raise ValueError("Invalid saved worker recovery policy")
        for key, maximum in (("remaining_retries", 6), ("retries_used", 100), ("backoff_seconds", 120), ("max_backoff_seconds", 300)):
            value = policy.get(key)
            if type(value) is not int or not 0 <= value <= maximum:
                raise ValueError("Invalid saved worker recovery budget")
        if type(policy.get("allow_output_changes")) is not bool or policy["max_backoff_seconds"] < policy["backoff_seconds"]:
            raise ValueError("Invalid saved worker recovery permissions")

    @classmethod
    def from_environment(cls):
        raw = os.environ.get("JOTIST_RECOVERY_POLICY", "")
        if not raw:
            return cls()
        value = json.loads(raw)
        if value is None:
            raise ValueError("Invalid saved worker recovery policy")
        return cls(value)

    def take(self, action, changes_output=False):
        if action not in {"decoder_budget_retry", "token_window_split", "native_timing_split", "native_timing_repair"}:
            raise ValueError("Unknown worker recovery action")
        if self.policy is None:
            return True
        if self.used >= self.policy["remaining_retries"] or changes_output and not self.policy["allow_output_changes"]:
            return False
        self.used += 1
        print("JOTIST_RECOVERY=" + json.dumps({"action": action, "retry": self.used}), flush=True)
        index = self.policy["retries_used"] + self.used
        delay = min(self.policy["max_backoff_seconds"], self.policy["backoff_seconds"] * (2 ** min(index - 1, 20)))
        if delay:
            time.sleep(delay)  # The supervised worker group is killed on cancel.
        return True


def progress():
    """Shared monotonic work evidence; no transcript, prompt or credentials."""
    global _completed_units
    _completed_units += 1
    print("JOTIST_PROGRESS=" + json.dumps({"completed_units": _completed_units}), flush=True)


def exception_chain(exc):
    seen = set()
    for _ in range(12):
        if exc is None or id(exc) in seen:
            break
        seen.add(id(exc))
        yield exc
        exc = exc.__cause__ or exc.__context__


def gpu_failure_kind(exc, device):
    if str(device).split(":")[0] != "cuda":
        return None
    chain = list(exception_chain(exc))
    message = " ".join(str(error).lower() for error in chain)
    # GPU scopes can include framework-managed downloads and audio decoding.
    # These failures do not become retryable merely because CUDA was selected.
    excluded = ("gated", "unauthorized", "forbidden", "401 client error", "403 client error", "http 401", "http 403",
                "invalid token", "access token", "authentication", "repository not found",
                "model access", "token is required", "missing token", "license agreement",
                "failed to load audio", "invalid audio", "audio input", "empty audio",
                "failed to decode", "error opening", "file not found", "no such file",
                "permission denied", "cancelled", "canceled", "deadline exceeded",
                "unsupported language", "invalid configuration")
    if any(value in message for value in excluded):
        return None
    if any(type(error).__name__ == "OutOfMemoryError" for error in chain) or "cuda out of memory" in message or "cuda error: out of memory" in message or "cublas_status_alloc_failed" in message:
        return "cuda_out_of_memory"
    return "cuda_runtime_error" if isinstance(exc, RuntimeError) else None


def host_failure_kind(exc, device):
    if str(device).split(":")[0] != "cpu":
        return None
    for error in exception_chain(exc):
        message = str(error).lower()
        if isinstance(error, MemoryError) or isinstance(error, RuntimeError) and "defaultcpuallocator" in message and "not enough memory" in message:
            return "host_out_of_memory"
    return None


@contextmanager
def gpu_execution(device):
    """Limit broad RuntimeError retries to an actual model execution scope."""
    try:
        yield
    except Exception as exc:
        if host_failure_kind(exc, device):
            print("JOTIST_HOST_FAILURE=" + json.dumps({"device": "cpu", "kind": "host_out_of_memory"}), flush=True)
        kind = gpu_failure_kind(exc, device)
        if kind:
            print("JOTIST_GPU_FAILURE=" + json.dumps({"device": "cuda", "kind": kind}), flush=True)
        raise
    else:
        progress()
