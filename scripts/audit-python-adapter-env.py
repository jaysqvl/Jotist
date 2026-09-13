#!/usr/bin/env python3
"""Audit installed package metadata against OSV without importing model runtimes.

Run with an adapter venv's Python after its import checks. Only public package
names and versions are sent to OSV. A successful result covers published package
advisories plus explicitly reported source-reviewed exceptions, not artifact or
checkpoint safety. Network failures and changed exception evidence fail closed.
"""
from __future__ import annotations

import argparse
from datetime import date
import hashlib
from importlib.metadata import distributions
import json
from pathlib import Path, PurePosixPath
import re
import sys
import sysconfig
from urllib.request import Request, urlopen


POLICY = Path(__file__).with_name("python-adapter-audit-policy.json")
OSV_URL = "https://api.osv.dev/v1/querybatch"
TORCH_PACKAGES = {"torch", "torchaudio", "torchvision", "torchcodec"}
REVIEWED_LOCAL_PORTS = {
    ("whisperx", "3.8.7rc1+jotist.1"): "3.8.7rc1",
    ("diarizen", "0.0.1+jotist.1"): "0.0.1",
    ("pyannote-audio", "3.1.1+jotist.1"): "3.1.1",
    ("nv-one-logger-pytorch-lightning-integration", "2.3.1+jotist.1"): "2.3.1",
}


class AuditError(Exception):
    pass


def canonical_name(name):
    return re.sub(r"[-_.]+", "-", name).lower()


def advisory_version(name, installed_version):
    """Conservatively audit known vendor builds against their public base version."""
    if canonical_name(name) in TORCH_PACKAGES:
        match = re.fullmatch(r"(\d+\.\d+\.\d+)\+(?:cpu|cu\d+)", installed_version)
        if match:
            return match.group(1)
    return REVIEWED_LOCAL_PORTS.get((canonical_name(name), installed_version), installed_version)


def installed_packages(sites):
    packages = {}
    for distribution in distributions(path=[str(site) for site in sites]):
        name, version = distribution.metadata.get("Name"), distribution.version
        if not name or not version:
            raise AuditError("Installed distribution has missing name/version metadata")
        name = canonical_name(name)
        if name in packages and packages[name]["installed_version"] != version:
            raise AuditError(f"Conflicting installed versions for {name}")
        packages[name] = {
            "name": name,
            "installed_version": version,
            "advisory_version": advisory_version(name, version),
        }
    if not packages:
        raise AuditError("No installed distributions found; refusing an empty audit")
    return [packages[name] for name in sorted(packages)]


def query_osv(packages):
    payload = {"queries": [{"package": {"name": p["name"], "ecosystem": "PyPI"},
                            "version": p["advisory_version"]} for p in packages]}
    request = Request(OSV_URL, data=json.dumps(payload).encode(),
                      headers={"Content-Type": "application/json", "User-Agent": "Jotist-runtime-audit/1"})
    try:
        with urlopen(request, timeout=30) as response:
            content = response.read(8_000_001)
        if len(content) > 8_000_000:
            raise ValueError("response exceeds size limit")
        result = json.loads(content)
        results = result["results"]
        if not isinstance(results, list) or len(results) != len(packages):
            raise ValueError("incomplete result count")
        for entry in results:
            if not isinstance(entry, dict) or "error" in entry:
                raise ValueError("invalid package result")
            if not isinstance(entry.get("vulns", []), list):
                raise ValueError("invalid advisory list")
            for vulnerability in entry.get("vulns", []):
                if not isinstance(vulnerability, dict) or not isinstance(vulnerability.get("id"), str):
                    raise ValueError("advisory without ID")
        return results
    except Exception as exc:
        raise AuditError(f"OSV audit unavailable or incomplete ({type(exc).__name__}); no clean result") from exc


def source_evidence_matches(checks, sites, within_roots=False):
    if not checks:
        return False
    for relative, expected_hash in checks.items():
        path = PurePosixPath(relative)
        if path.is_absolute() or ".." in path.parts or not re.fullmatch(r"[0-9a-f]{64}", expected_hash):
            return False
        candidates = [site / relative for site in sites if (site / relative).is_file()]
        if not candidates:
            return False
        if within_roots and any(
            not (site / relative).resolve().is_relative_to(site.resolve())
            for site in sites if (site / relative).is_file()
        ):
            return False
        # Reject ambiguity if two import roots supply different source bytes.
        if any(hashlib.sha256(candidate.read_bytes()).hexdigest() != expected_hash for candidate in candidates):
            return False
    return True


def matching_exception(package, advisory_id, adapter, policy, sites, today=None, caller_root=None):
    today = today or date.today()
    for exception in policy.get("exceptions", []):
        if (canonical_name(exception["package"]) != package["name"]
                or exception["version"] != package["installed_version"]
                or advisory_id not in exception["advisory_ids"]
                or adapter not in exception["adapters"]):
            continue
        if today > date.fromisoformat(exception["review_by"]):
            continue
        if not exception.get("reason") or not exception.get("references"):
            continue
        if "caller_sha256" in exception and (
            caller_root is None or not source_evidence_matches(
                exception["caller_sha256"], [caller_root], within_roots=True
            )
        ):
            continue
        if source_evidence_matches(exception.get("source_sha256", {}), sites):
            return exception
    return None


def classify_results(packages, results, adapter, policy, sites, today=None, caller_root=None):
    findings = []
    for package, result in zip(packages, results):
        for vulnerability in result.get("vulns", []):
            advisory_id = vulnerability["id"]
            exception = matching_exception(package, advisory_id, adapter, policy, sites, today, caller_root)
            finding = {**package, "advisory_id": advisory_id,
                       "status": "reviewed_exception" if exception else "needs_review",
                       "advisory_url": f"https://osv.dev/vulnerability/{advisory_id}"}
            if exception:
                finding.update({"policy_id": exception["id"], "reason": exception["reason"],
                                "review_by": exception["review_by"], "references": exception["references"],
                                "verified_source_files": list(exception["source_sha256"]),
                                "verified_caller_files": list(exception.get("caller_sha256", {}))})
            findings.append(finding)
    return findings


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--adapter", required=True, help="Exact adapter key from the environment verifier")
    parser.add_argument("--site-packages", action="append", type=Path,
                        help="Explicit installed metadata root; defaults to this venv's site-packages")
    parser.add_argument("--policy", type=Path, default=POLICY)
    parser.add_argument("--caller-root", type=Path,
                        help="Actual materialized adapter directory; required for caller-bound exceptions")
    parser.add_argument("--output", type=Path, help="Write complete package inventory and findings as JSON")
    args = parser.parse_args(argv)
    report = {"adapter": args.adapter, "service": OSV_URL, "checked_on": date.today().isoformat()}
    try:
        policy = json.loads(args.policy.read_text())
        if policy.get("schema_version") != 2 or args.adapter not in policy["adapters"]:
            raise AuditError("Unknown adapter or unsupported audit policy")
        if not args.site_packages and sys.prefix == sys.base_prefix:
            raise AuditError("Run with the adapter venv Python or provide --site-packages explicitly")
        sites = args.site_packages or sorted({Path(sysconfig.get_path("purelib")), Path(sysconfig.get_path("platlib"))})
        if any(not site.is_dir() for site in sites):
            raise AuditError("Installed metadata directory is missing")
        if args.caller_root is not None and not args.caller_root.is_dir():
            raise AuditError("Application caller directory is missing")
        packages = installed_packages(sites)
        findings = classify_results(packages, query_osv(packages), args.adapter, policy, sites,
                                    caller_root=args.caller_root)
        unreviewed = [f for f in findings if f["status"] == "needs_review"]
        report.update({"status": "needs_review" if unreviewed else "passed",
                       "packages": packages, "findings": findings})
        print(f"{args.adapter}: {len(packages)} installed packages; {len(unreviewed)} unreviewed advisories; "
              f"{len(findings) - len(unreviewed)} source-verified policy matches")
        for finding in findings:
            print(f"  {finding['status']}: {finding['name']}=={finding['installed_version']} "
                  f"{finding['advisory_id']}" + (f" [{finding['policy_id']}]" if "policy_id" in finding else ""))
        return_code = 1 if unreviewed else 0
    except (AuditError, OSError, ValueError, KeyError, TypeError) as exc:
        report.update({"status": "error", "error": str(exc)})
        print(f"{args.adapter}: audit failed: {exc}", file=sys.stderr)
        return_code = 2
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, indent=2) + "\n")
    return return_code


if __name__ == "__main__":
    raise SystemExit(main())
