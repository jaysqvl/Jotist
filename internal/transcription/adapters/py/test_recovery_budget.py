"""Policy and retry limits without model imports, inference or downloads."""
import importlib.util
import json
from pathlib import Path

import pytest

spec = importlib.util.spec_from_file_location("recovery_budget_test_runtime", Path(__file__).with_name("runtime_failure.py"))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)


def policy(remaining=3, output=False, used=0):
    return {"version": 1, "remaining_retries": remaining, "retries_used": used,
            "backoff_seconds": 2, "max_backoff_seconds": 6, "allow_output_changes": output}


def test_one_budget_and_backoff_are_shared_by_all_inner_actions(monkeypatch, capsys):
    delays = []
    monkeypatch.setattr(runtime.time, "sleep", delays.append)
    budget = runtime.RecoveryBudget(policy(2, output=True, used=1))
    assert budget.take("decoder_budget_retry")
    assert budget.take("token_window_split", changes_output=True)
    assert not budget.take("native_timing_repair", changes_output=True)
    assert delays == [4, 6]
    records = [json.loads(line.split("=", 1)[1]) for line in capsys.readouterr().out.splitlines()]
    assert records == [{"action": "decoder_budget_retry", "retry": 1}, {"action": "token_window_split", "retry": 2}]


def test_standard_forbids_output_changes_without_consuming_a_retry(monkeypatch, capsys):
    monkeypatch.setattr(runtime.time, "sleep", lambda _: None)
    budget = runtime.RecoveryBudget(policy())
    assert not budget.take("token_window_split", changes_output=True)
    assert not budget.take("native_timing_repair", changes_output=True)
    assert budget.used == 0
    assert budget.take("decoder_budget_retry")
    assert "private" not in capsys.readouterr().out
    assert not runtime.RecoveryBudget(policy(0, output=True)).take("decoder_budget_retry")


def test_environment_is_validated_and_absent_policy_preserves_legacy(monkeypatch):
    monkeypatch.delenv("JOTIST_RECOVERY_POLICY", raising=False)
    assert runtime.RecoveryBudget.from_environment().take("token_window_split", changes_output=True)
    for raw in ("{", "null", json.dumps({**policy(), "version": True}), json.dumps({**policy(), "remaining_retries": 7}), json.dumps({**policy(), "allow_output_changes": "true"})):
        monkeypatch.setenv("JOTIST_RECOVERY_POLICY", raw)
        with pytest.raises((ValueError, TypeError)):
            runtime.RecoveryBudget.from_environment()
