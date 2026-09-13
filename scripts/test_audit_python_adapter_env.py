"""Offline audit-policy regression checks: no runtime imports or network calls."""
from datetime import date
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("adapter_audit", Path(__file__).with_name("audit-python-adapter-env.py"))
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


class AuditPolicyTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.site = Path(self.directory.name)
        (self.site / "saving.py").write_text("reviewed source\n")
        self.package = {"name": "lightning", "installed_version": "2.6.6", "advisory_version": "2.6.6"}
        self.policy = {"exceptions": [{
            "id": "reviewed-fix", "package": "lightning", "version": "2.6.6",
            "advisory_ids": ["GHSA-known"], "adapters": ["whisperx"],
            "review_by": "2026-10-13", "reason": "Reviewed specific source fix",
            "references": ["https://example.invalid/advisory"],
            "source_sha256": {"saving.py": hashlib.sha256(b"reviewed source\n").hexdigest()},
        }]}

    def match(self, package=None, advisory="GHSA-known", adapter="whisperx", today=date(2026, 9, 13), caller_root=None):
        return audit.matching_exception(package or self.package, advisory, adapter, self.policy, [self.site], today, caller_root)

    def test_exception_requires_exact_package_version_advisory_and_adapter(self):
        self.assertIsNotNone(self.match())
        for field, value in [("name", "pytorch-lightning"), ("installed_version", "2.6.5")]:
            self.assertIsNone(self.match({**self.package, field: value}))
        self.assertIsNone(self.match(advisory="GHSA-new"))
        self.assertIsNone(self.match(adapter="canary-qwen"))
        self.assertIsNone(self.match(today=date(2026, 10, 14)))

    def test_changed_or_missing_source_invalidates_exception(self):
        (self.site / "saving.py").write_text("different source\n")
        self.assertIsNone(self.match())
        (self.site / "saving.py").unlink()
        self.assertIsNone(self.match())
        self.assertFalse(audit.source_evidence_matches({}, [self.site]))

    def test_changed_materialized_caller_invalidates_unchanged_dependency_exception(self):
        caller_root = self.site / "actual-adapter"
        caller_root.mkdir()
        caller = caller_root / "transcribe.py"
        reviewed = b'model = load_model("fixed/vendor-model")\n'
        caller.write_bytes(reviewed)
        self.policy["exceptions"][0]["caller_sha256"] = {
            "transcribe.py": hashlib.sha256(reviewed).hexdigest()
        }
        self.assertIsNone(self.match())
        self.assertIsNone(self.match(caller_root=self.site))
        self.assertIsNotNone(self.match(caller_root=caller_root))

        # Package versions and installed dependency source remain unchanged.
        caller.write_text('model = load_model(request["model"])\n')
        findings = audit.classify_results(
            [self.package], [{"vulns": [{"id": "GHSA-known"}]}], "whisperx",
            self.policy, [self.site], date(2026, 9, 13), caller_root,
        )
        self.assertEqual(findings[0]["status"], "needs_review")
        caller.unlink()
        self.assertIsNone(self.match(caller_root=caller_root))
        outside = self.site / "reviewed-but-not-deployed.py"
        outside.write_bytes(reviewed)
        caller.symlink_to(outside)
        self.assertIsNone(self.match(caller_root=caller_root))

    def test_cuda_versions_query_public_release_without_widening_exception(self):
        for package in ["torch", "torchaudio", "torchvision", "torchcodec"]:
            self.assertEqual(audit.advisory_version(package, "2.14.0+cu126"), "2.14.0")
            self.assertEqual(audit.advisory_version(package, "2.14.0+cu130"), "2.14.0")
            self.assertEqual(audit.advisory_version(package, "2.14.0+cpu"), "2.14.0")
        self.assertEqual(audit.advisory_version("other", "2.14.0+cu126"), "2.14.0+cu126")
        self.assertEqual(audit.advisory_version("torch", "2.14.0+custom"), "2.14.0+custom")
        self.assertIsNone(self.match({**self.package, "installed_version": "2.6.6+custom"}))

    def test_new_advisory_stays_unreviewed_beside_known_match(self):
        findings = audit.classify_results([self.package], [{"vulns": [{"id": "GHSA-known"}, {"id": "GHSA-new"}]}],
                                         "whisperx", self.policy, [self.site], date(2026, 9, 13))
        self.assertEqual([f["status"] for f in findings], ["reviewed_exception", "needs_review"])
        self.assertEqual(findings[0]["policy_id"], "reviewed-fix")

    def test_reviewed_local_ports_cannot_hide_upstream_advisories(self):
        for (package, installed), expected in audit.REVIEWED_LOCAL_PORTS.items():
            self.assertEqual(audit.advisory_version(package, installed), expected)
        self.assertEqual(audit.advisory_version("pyannote.audio", "3.1.1+jotist.1"), "3.1.1")
        self.assertEqual(audit.advisory_version("other", "0.0.1+jotist.1"), "0.0.1+jotist.1")
        self.assertEqual(audit.advisory_version("diarizen", "0.0.1+jotist.2"), "0.0.1+jotist.2")

    def test_network_failure_never_returns_clean_results(self):
        with patch.object(audit, "urlopen", side_effect=TimeoutError("offline")):
            with self.assertRaises(audit.AuditError):
                audit.query_osv([self.package])


if __name__ == "__main__":
    unittest.main()
