#!/usr/bin/env python3
"""Prepare narrowly scoped OpenVEX only after actual image evidence matches.

Trivy still enforces HIGH/CRITICAL findings, including unfixed and new CVEs.
The original scan, package proof and generated VEX are retained together.
"""

import argparse
from datetime import date, datetime, timezone
import hashlib
import json
from pathlib import Path
import re


POLICY = Path(__file__).with_name("container-header-review.json")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def manifest_hash(paths):
    require(isinstance(paths, list) and paths and len(paths) == len(set(paths)),
            "missing or ambiguous installed paths")
    require(all(isinstance(p, str) and p.startswith("/") for p in paths),
            "invalid installed paths")
    return hashlib.sha256(("\n".join(sorted(paths)) + "\n").encode()).hexdigest()


def build_vex(policy, report, smoke, today=None):
    today = today or datetime.now(timezone.utc).date()
    require(policy.get("schema_version") == 1, "unsupported applicability policy")
    require(date.fromisoformat(policy["review_by"]) >= today, "package review expired")
    reviewed_at = datetime.fromisoformat(policy["reviewed_at"].replace("Z", "+00:00"))
    require(reviewed_at.tzinfo is not None and reviewed_at.astimezone(timezone.utc).date() <= today,
            "package review is future dated")
    package = policy["package"]
    require(re.fullmatch(r"pkg:deb/ubuntu/linux-libc-dev@6\.8\.0-\d+\.\d+\?arch=amd64&distro=ubuntu-24\.04",
                         package["purl"]) is not None, "unbounded package scope")
    require(policy["justification"] == "vulnerable_code_not_present" and policy["impact"]
            and policy["references"], "missing applicability rationale")
    require(report.get("SchemaVersion") == 2 and report.get("ArtifactType") == "container_image",
            "expected a complete image scan")
    require(report.get("Metadata", {}).get("OS") == {"Family": "ubuntu", "Name": "24.04"},
            "image OS differs from reviewed package")
    require(smoke.get("passed") is True and report.get("ArtifactName") == smoke.get("image"),
            "scan and successful image qualification differ")
    packages = [p for result in report.get("Results", [])
                if result.get("Class") == "os-pkgs" and result.get("Type") == "ubuntu"
                for p in result.get("Packages", [])
                if p.get("Identifier", {}).get("PURL") == package["purl"]]
    require(len(packages) == 1, "reviewed package missing or ambiguous in image scan")
    installed = packages[0]
    version = installed.get("Version", "")
    if installed.get("Release"):
        version += "-" + installed["Release"]
    if installed.get("Epoch"):
        version = str(installed["Epoch"]) + ":" + version
    require(installed.get("Name") == package["name"] and version == package["version"],
            "installed package identity differs")
    require(manifest_hash(installed.get("InstalledFiles")) == package["installed_paths_sha256"],
            "installed path manifest changed")
    require(smoke.get("cases"), "image has no qualified identities")
    for case in smoke["cases"]:
        evidence = case.get("kernel_headers", {})
        require(evidence.get("regular_headers_and_docs_only") is True
                and all(evidence.get(key) == package[key] for key in
                        ("name", "version", "installed_files", "headers", "installed_paths_sha256")),
                "actual image header payload proof missing or changed")

    exported = policy["exported_header_change"]
    require(exported["exported_before_sha256"] == exported["exported_after_sha256"]
            and re.fullmatch(r"[0-9a-f]{64}", exported["exported_before_sha256"])
            and exported["reason"], "exported header proof missing")
    statements = []
    for cve, fix in sorted(policy["upstream_fixes"].items()):
        require(re.fullmatch(r"CVE-\d{4}-\d{4,}", cve) and re.fullmatch(r"[0-9a-f]{40}", fix["commit"])
                and fix["paths"], "missing reviewed upstream fix")
        for path in fix["paths"]:
            require(path not in installed["InstalledFiles"] and "/" + path not in installed["InstalledFiles"],
                    "upstream fix changes shipped package payload")
            if "/uapi/" in path:
                require(cve == exported["cve"] and path == exported["path"]
                        and fix["commit"] == exported["fix_commit"], "unreviewed exported header change")
        statements.append({
            "vulnerability": {"name": cve},
            "products": [{"@id": package["purl"]}],
            "status": "not_affected",
            "justification": policy["justification"],
            "impact_statement": policy["impact"] + (" " + exported["reason"] if cve == exported["cve"] else ""),
            "status_notes": "Upstream fix: https://github.com/gregkh/linux/commit/" + fix["commit"]
                            + "; changed kernel paths: " + ", ".join(fix["paths"]),
        })
    require(statements, "empty package review")
    digest = hashlib.sha256(json.dumps(policy, sort_keys=True).encode()).hexdigest()
    return {
        "@context": "https://openvex.dev/ns/v0.2.0",
        "@id": "https://github.com/jaysqvl/Jotist/security/vex/" + digest,
        "author": "https://github.com/jaysqvl/Jotist",
        "timestamp": policy["reviewed_at"],
        "version": 1,
        "statements": statements,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", type=Path, required=True)
    parser.add_argument("--smoke", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    policy = json.loads(POLICY.read_text())
    vex = build_vex(policy, json.loads(args.report.read_text()), json.loads(args.smoke.read_text()))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(vex, indent=2) + "\n")
    print(json.dumps({"reviewed_package": policy["package"]["purl"],
                      "review_by": policy["review_by"], "statements": len(vex["statements"])}))


if __name__ == "__main__":
    main()
