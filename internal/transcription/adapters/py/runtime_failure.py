"""Safe GPU execution evidence. No ML imports or user data in failure records."""
from contextlib import contextmanager
import json


def gpu_failure_kind(exc, device):
    if str(device).split(":")[0] != "cuda" or not isinstance(exc, RuntimeError):
        return None
    message = str(exc).lower()
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
    if type(exc).__name__ == "OutOfMemoryError" or "cuda out of memory" in message or "cuda error: out of memory" in message or "cublas_status_alloc_failed" in message:
        return "cuda_out_of_memory"
    return "cuda_runtime_error"


@contextmanager
def gpu_execution(device):
    """Limit broad RuntimeError retries to an actual model execution scope."""
    try:
        yield
    except MemoryError:
        if str(device).split(":")[0] == "cpu":
            print("SCRIBERR_HOST_FAILURE=" + json.dumps({"device": "cpu", "kind": "host_out_of_memory"}), flush=True)
        raise
    except RuntimeError as exc:
        if str(device).split(":")[0] == "cpu" and ("defaultcpuallocator" in str(exc).lower() and "not enough memory" in str(exc).lower()):
            print("SCRIBERR_HOST_FAILURE=" + json.dumps({"device": "cpu", "kind": "host_out_of_memory"}), flush=True)
        kind = gpu_failure_kind(exc, device)
        if kind:
            print("SCRIBERR_GPU_FAILURE=" + json.dumps({"device": "cuda", "kind": kind}), flush=True)
        raise
