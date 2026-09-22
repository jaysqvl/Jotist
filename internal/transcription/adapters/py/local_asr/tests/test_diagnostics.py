from pathlib import Path
import sys
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from backends import CohereAutoTokenLimitError, GenerationTokenLimitError, RecognitionError
from diagnostics import safe_failure


def test_wrapped_model_access_error_is_actionable_and_private():
    cause = RuntimeError("hf_private and private transcript")
    cause.response = SimpleNamespace(status_code=401)
    exc = OSError("private URL and token")
    exc.__cause__ = cause
    result = safe_failure(exc, "model_loading")
    assert result["diagnostic_code"] == "runtime_model_access"
    assert "Hugging Face token in Settings" in result["error"]
    assert "private" not in str(result)


def test_only_known_application_messages_are_saved():
    result = safe_failure(RecognitionError("CPU inference requires float32 precision."), "recognition")
    assert result["error"] == "CPU inference requires float32 precision."
    unknown = safe_failure(RecognitionError("private transcript hf_private"), "private phase")
    assert "private" not in str(unknown)
    assert unknown["phase"] == "configuration"


def test_token_cutoff_uses_catalog_message_and_bounded_window_coordinates():
    for error, code, limit in ((GenerationTokenLimitError(400), "application_a033c6a30bfc", 400),
                               (CohereAutoTokenLimitError(1000), "application_8923ea1c9bdf", 1000)):
        error.window_index, error.window_count = 17, 82
        result = safe_failure(error, "recognition")
        assert result["diagnostic_code"] == code
        assert result["window_index"] == 17 and result["window_count"] == 82
        assert result["token_limit"] == limit
        assert "private" not in str(result)

    invalid = RecognitionError("private transcript")
    invalid.window_index, invalid.window_count = 200000, 200000
    result = safe_failure(invalid, "recognition")
    assert "private" not in str(result)
    assert "window_index" not in result
