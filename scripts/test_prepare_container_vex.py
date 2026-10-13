"""Package applicability must not become a general vulnerability bypass."""

from copy import deepcopy
from datetime import date, datetime, timezone
import json
import unittest
from unittest.mock import patch

from prepare_container_vex import POLICY, build_vex, manifest_hash


class ContainerVexTests(unittest.TestCase):
    def setUp(self):
        self.policy = json.loads(POLICY.read_text())
        # Keep date-boundary fixtures stable when the real package review changes.
        self.policy.update(reviewed_at="2026-10-10T04:09:06Z", review_by="2026-11-09")
        self.cve = "CVE-2026-53398"
        self.policy["upstream_fixes"] = {self.cve: self.policy["upstream_fixes"][self.cve]}
        self.package = self.policy["package"]
        paths = ["/usr/include/linux/example.h", "/usr/share/doc/linux-libc-dev/copyright"]
        self.package.update(installed_paths_sha256=manifest_hash(paths), installed_files=2, headers=1)
        version, release = self.package["version"].rsplit("-", 1)
        self.report = {
            "SchemaVersion": 2, "ArtifactType": "container_image", "ArtifactName": "owned-image",
            "Metadata": {"OS": {"Family": "ubuntu", "Name": "24.04"}},
            "Results": [{"Class": "os-pkgs", "Type": "ubuntu", "Packages": [{
                "Name": self.package["name"], "Version": version, "Release": release,
                "Identifier": {"PURL": self.package["purl"]}, "InstalledFiles": paths,
            }], "Vulnerabilities": [{"VulnerabilityID": self.cve, "PkgName": "linux-libc-dev",
                                      "Severity": "CRITICAL", "Status": "affected"}]}],
        }
        evidence = {key: self.package[key] for key in
                    ("name", "version", "installed_files", "headers", "installed_paths_sha256",
                     "installed_payload_sha256")}
        evidence["regular_headers_and_docs_only"] = True
        self.smoke = {"passed": True, "image": "owned-image",
                      "cases": [{"kernel_headers": evidence}, {"kernel_headers": deepcopy(evidence)}]}

    def prepare(self):
        return build_vex(self.policy, self.report, self.smoke, date(2026, 10, 10))

    def test_review_requires_successful_actual_payload_evidence(self):
        statement = self.prepare()["statements"][0]
        self.assertEqual(statement["vulnerability"]["name"], self.cve)
        self.assertEqual(statement["products"], [{"@id": self.package["purl"]}])
        self.assertEqual(statement["justification"], "vulnerable_code_not_present")

    def test_expired_or_future_review_fails_closed(self):
        for key, value in (("review_by", "2026-10-09"), ("reviewed_at", "2026-10-11T00:00:00Z")):
            with self.subTest(key=key):
                policy = deepcopy(self.policy)
                policy[key] = value
                with self.assertRaises(ValueError):
                    build_vex(policy, self.report, self.smoke, date(2026, 10, 10))

    def test_review_dates_use_utc_even_when_local_calendar_day_differs(self):
        with patch("prepare_container_vex.datetime", wraps=datetime) as clock:
            clock.now.return_value = datetime(2026, 10, 10, 4, 9, tzinfo=timezone.utc)
            self.assertTrue(build_vex(self.policy, self.report, self.smoke)["statements"])
            clock.now.assert_called_once_with(timezone.utc)

    def test_changed_package_version_distro_or_architecture_does_not_match(self):
        installed = self.report["Results"][0]["Packages"][0]
        for old, new in ((self.package["version"], "6.8.0-999.999"), ("amd64", "arm64"), ("ubuntu-24.04", "ubuntu-26.04")):
            with self.subTest(new=new):
                installed["Identifier"]["PURL"] = self.package["purl"].replace(old, new)
                with self.assertRaises(ValueError):
                    self.prepare()

    def test_policy_cannot_omit_version_or_qualifiers_or_change_package(self):
        original = self.package["purl"]
        for value in (original.split("?")[0], "pkg:deb/ubuntu/linux-libc-dev", original.replace("linux-libc-dev", "linux-image")):
            self.package["purl"] = value
            with self.assertRaises(ValueError):
                self.prepare()

    def test_missing_or_changed_scan_or_actual_payload_fails_closed(self):
        cases = [
            lambda r, s: r["Results"][0]["Packages"].clear(),
            lambda r, s: r["Results"][0]["Packages"][0].update(Release="999.999"),
            lambda r, s: r["Results"][0]["Packages"][0]["InstalledFiles"].append("/boot/vmlinuz"),
            lambda r, s: r.update(ArtifactName="another-image"),
            lambda r, s: s.update(passed=False),
            lambda r, s: s.update(cases=[]),
            lambda r, s: s["cases"][0].pop("kernel_headers"),
            lambda r, s: s["cases"][1]["kernel_headers"].update(regular_headers_and_docs_only=False),
            lambda r, s: s["cases"][1]["kernel_headers"].update(installed_paths_sha256="0" * 64),
            lambda r, s: s["cases"][1]["kernel_headers"].update(installed_payload_sha256="0" * 64),
            lambda r, s: s["cases"][1]["kernel_headers"].pop("installed_payload_sha256"),
        ]
        for mutation in cases:
            report, smoke = deepcopy(self.report), deepcopy(self.smoke)
            mutation(report, smoke)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                build_vex(self.policy, report, smoke, date(2026, 10, 10))

    def test_policy_cannot_omit_reviewed_file_content_proof(self):
        self.package.pop("installed_payload_sha256")
        with self.assertRaisesRegex(ValueError, "content hash"):
            self.prepare()

    def test_new_cve_and_same_cve_on_another_package_receive_no_exemption(self):
        self.report["Results"][0]["Vulnerabilities"].extend([
            {"VulnerabilityID": "CVE-2026-99999", "PkgName": "linux-libc-dev", "Severity": "HIGH"},
            {"VulnerabilityID": self.cve, "PkgName": "linux-image", "Severity": "CRITICAL"},
        ])
        statements = self.prepare()["statements"]
        self.assertEqual([s["vulnerability"]["name"] for s in statements], [self.cve])
        self.assertEqual([p["@id"] for s in statements for p in s["products"]], [self.package["purl"]])

    def test_unreviewed_exported_header_change_is_rejected(self):
        self.policy["upstream_fixes"][self.cve]["paths"].append("include/uapi/linux/new.h")
        with self.assertRaisesRegex(ValueError, "unreviewed exported"):
            self.prepare()

    def test_fix_cannot_change_a_shipped_payload_file(self):
        self.policy["upstream_fixes"][self.cve]["paths"].append("usr/include/linux/example.h")
        with self.assertRaisesRegex(ValueError, "shipped package payload"):
            self.prepare()

    def test_reviewed_kernel_only_header_change_keeps_exact_export_proof(self):
        policy = json.loads(POLICY.read_text())
        self.policy["upstream_fixes"] = {"CVE-2026-80668": policy["upstream_fixes"]["CVE-2026-80668"]}
        self.assertIn("__KERNEL__", self.prepare()["statements"][0]["impact_statement"])
        self.policy["exported_header_change"]["exported_after_sha256"] = "0" * 64
        with self.assertRaises(ValueError):
            self.prepare()


if __name__ == "__main__":
    unittest.main()
