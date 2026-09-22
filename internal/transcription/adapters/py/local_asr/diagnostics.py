"""Closed, application-owned worker diagnostics: never persist exception text."""
import json
from pathlib import Path

_MESSAGES = json.loads(Path(__file__).with_name("diagnostics.json").read_text(encoding="utf-8"))
_APPLICATION_CODES = {message: code for code, message in _MESSAGES.items() if code.startswith("application_")}
_CLASSES = {"RecognitionError", "RuntimeError", "ValueError", "TypeError", "AttributeError", "KeyError", "IndexError", "ImportError", "ModuleNotFoundError", "OSError", "FileNotFoundError", "PermissionError", "MemoryError", "GatedRepoError", "RepositoryNotFoundError", "HfHubHTTPError"}
_PHASES = {"configuration", "runtime_initialization", "audio_decode", "model_loading", "recognition", "alignment", "output_validation"}


def safe_failure(exc, phase):
    from backends import RecognitionError
    kind = type(exc).__name__
    code = _APPLICATION_CODES.get(str(exc)) if isinstance(exc, RecognitionError) else None
    # Transformers wraps Hugging Face access errors in OSError. Inspect the
    # cause classes/status only; library error text may contain credentials.
    chain, current = [], exc
    while current is not None and len(chain) < 12 and all(current is not item for item in chain):
        chain.append(current)
        current = current.__cause__ or current.__context__
    if code is None and any(type(item).__name__ in {"GatedRepoError", "RepositoryNotFoundError", "HfHubHTTPError"}
                            or getattr(getattr(item, "response", None), "status_code", None) in {401, 403}
                            for item in chain):
        code = "runtime_model_access"
    if code is None:
        if isinstance(exc, (ImportError, ModuleNotFoundError)):
            code = "runtime_dependency_missing"
        elif isinstance(exc, (TypeError, AttributeError, KeyError, IndexError)):
            code = "runtime_api_incompatible"
        elif kind in {"GatedRepoError", "RepositoryNotFoundError", "HfHubHTTPError"}:
            code = "runtime_model_access"
        elif isinstance(exc, (PermissionError, FileNotFoundError)):
            code = "runtime_file_access"
        elif isinstance(exc, MemoryError):
            code = "runtime_memory_error"
        else:
            code = "runtime_model_error"
    result = {"diagnostic_code": code, "error": _MESSAGES[code],
              "exception_class": kind if kind in _CLASSES else "ModelError",
              "phase": phase if phase in _PHASES else "configuration"}
    if phase == "recognition" and isinstance(exc, RecognitionError):
        index, count = getattr(exc, "window_index", None), getattr(exc, "window_count", None)
        if type(index) is int and type(count) is int and 1 <= index <= count <= 100000:
            result.update(window_index=index, window_count=count)
        limit = getattr(exc, "token_limit", None)
        if code in {"application_a033c6a30bfc", "application_8923ea1c9bdf"} and type(limit) is int and 1 <= limit <= 65536:
            result["token_limit"] = limit
    return result
