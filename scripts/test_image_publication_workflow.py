"""Exercise publication gates, including GitHub's implicit success condition.

This covers the flat job expressions used by our publisher, not a general YAML
or Actions interpreter. actionlint validates the complete workflow separately.
GitHub applies success() unless an if expression uses a status-check function:
https://docs.github.com/en/actions/reference/workflows-and-actions/expressions
"""

import json
import os
from pathlib import Path
import re
import subprocess
import unittest


WORKFLOW = Path(__file__).resolve().parents[1] / ".github/workflows/docker-dev-images.yml"


def job(name):
    match = re.search(rf"^  {re.escape(name)}:\n(.*?)(?=^  \w+:|\Z)", WORKFLOW.read_text(), re.M | re.S)
    if not match:
        raise AssertionError(f"Missing publication job: {name}")
    return match.group(1)


def runs(name, results, *, reused=False, cancelled=False):
    match = re.search(r"^    if: (.+)$", job(name), re.M)
    expression = match.group(1) if match else "success()"
    expression = expression.removeprefix("${{").removesuffix("}}").strip()
    implicit_success = not cancelled and all(result == "success" for result in results.values())
    has_status_check = re.search(r"\b(?:always|cancelled|success|failure)\s*\(", expression)
    expression = expression.replace("needs.resolve.outputs.already_validated", repr("true" if reused else "false"))
    expression = re.sub(r"needs\.(\w+)\.result", lambda m: repr(results[m[1]]), expression)
    for function, value in (("cancelled()", cancelled), ("success()", implicit_success),
                            ("failure()", "failure" in results.values()), ("always()", True)):
        expression = expression.replace(function, str(value))
    expression = expression.replace("&&", " and ").replace("||", " or ")
    expression = re.sub(r"!(?!=)", " not ", expression)
    return bool(eval(expression.strip(), {"__builtins__": {}}, {})) and (bool(has_status_check) or implicit_success)


class ImagePublicationWorkflowTests(unittest.TestCase):
    def test_promotes_after_reusing_completed_source_validation(self):
        self.assertTrue(runs("promote", {"resolve": "success", "validate": "skipped", "qualify": "success"}))

    def test_promotes_after_direct_source_validation(self):
        self.assertTrue(runs("promote", {"resolve": "success", "validate": "success", "qualify": "success"}))

    def test_promotion_rejects_failed_skipped_or_cancelled_prerequisites(self):
        for prerequisite in ("resolve", "qualify"):
            for result in ("failure", "skipped", "cancelled"):
                with self.subTest(prerequisite=prerequisite, result=result):
                    results = {"resolve": "success", "validate": "skipped", "qualify": "success"}
                    results[prerequisite] = result
                    self.assertFalse(runs("promote", results))
        self.assertFalse(runs("promote", {"resolve": "success", "qualify": "success"}, cancelled=True))

    def test_qualification_requires_validation_or_exact_validated_source_reuse(self):
        for result in ("success", "failure", "skipped", "cancelled"):
            for reused in (False, True):
                with self.subTest(result=result, reused=reused):
                    expected = result == "success" or (result == "skipped" and reused)
                    self.assertEqual(runs("qualify", {"resolve": "success", "validate": result}, reused=reused), expected)

    def test_completion_gate_runs_after_a_skipped_promotion_and_fails(self):
        self.assertTrue(runs("complete", {"resolve": "success", "qualify": "success", "promote": "skipped", "validate": "skipped"}))
        self.assertFalse(runs("complete", {"resolve": "success", "qualify": "success", "promote": "success"}, cancelled=True))
        block = job("complete")
        self.assertRegex(block, r"needs: \[resolve, qualify, promote\]")
        script = re.search(r"^        run: \|\n((?:^          .*\n|^\n)+)", block, re.M)
        self.assertIsNotNone(script)
        command = "\n".join(line[10:] for line in script[1].splitlines())
        for prerequisite in ("resolve", "qualify", "promote"):
            for result in ("success", "failure", "skipped", "cancelled"):
                with self.subTest(prerequisite=prerequisite, result=result):
                    results = {name: {"result": "success"} for name in ("resolve", "qualify", "promote")}
                    results[prerequisite]["result"] = result
                    completed = subprocess.run(["bash", "-e", "-c", command],
                                               env=os.environ | {"RESULTS": json.dumps(results)},
                                               capture_output=True, text=True, check=False)
                    self.assertEqual(completed.returncode == 0, result == "success")


if __name__ == "__main__":
    unittest.main()
