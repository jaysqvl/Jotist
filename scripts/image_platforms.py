"""Validate per-architecture release evidence before assembling or promoting tags."""

import argparse
from collections import Counter
import json
from pathlib import Path
import re


def require(condition, message):
    if not condition:
        raise ValueError(message)


def qualified_records(directory, matrix, repository, revision):
    require(re.fullmatch(r"ghcr\.io/[a-z0-9_.-]+/jotist", repository), "invalid repository")
    require(re.fullmatch(r"[0-9a-f]{40}", revision), "invalid source revision")
    rows = matrix["include"]
    require(rows and len({(r["variant"], r["architecture"]) for r in rows}) == len(rows), "duplicate or empty qualification matrix")
    records = []
    for row in rows:
        variant, arch = row["variant"], row["architecture"]
        require(variant in {"cpu", "cuda", "blackwell"} and arch in {"amd64", "arm64"}, "unsupported image target")
        require(variant == "cpu" or arch == "amd64", "ARM GPU images are not qualified")
        record = json.loads((directory / f"qualified-{variant}-{arch}.json").read_text())
        require(all(record.get(key) == value for key, value in {
            "repository": repository, "revision": revision, "variant": variant,
            "architecture": arch, "suffix": row["suffix"],
        }.items()), "qualification identity differs from the requested target")
        require(re.fullmatch(r"sha256:[0-9a-f]{64}", record.get("digest", "")), "invalid qualified digest")
        records.append(record)
    for variant in {r["variant"] for r in rows}:
        expected = {"amd64", "arm64"} if variant == "cpu" else {"amd64"}
        require({r["architecture"] for r in rows if r["variant"] == variant} == expected,
                "requested variant is missing a required architecture")
    return records


def assembled_records(directory, matrix, repository, revision):
    leaves = qualified_records(directory, matrix, repository, revision)
    result = []
    for variant in sorted({r["variant"] for r in leaves}):
        sources = [r for r in leaves if r["variant"] == variant]
        record = json.loads((directory / f"assembled-{variant}.json").read_text())
        require(all(record.get(key) == value for key, value in {
            "repository": repository, "revision": revision, "variant": variant,
            "suffix": sources[0]["suffix"],
            "sources": {r["architecture"]: r["digest"] for r in sources},
        }.items()), "assembled image does not match its qualified source images")
        require(re.fullmatch(r"sha256:[0-9a-f]{64}", record.get("digest", "")), "invalid assembled digest")
        result.append(record)
    return result


def verify_platforms(manifest, architectures):
    actual = []
    for descriptor in manifest.get("manifests", []):
        platform = descriptor.get("platform", {})
        if platform == {"architecture": "unknown", "os": "unknown"}:
            require(descriptor.get("annotations", {}).get("vnd.docker.reference.type") == "attestation-manifest",
                    "unknown platform is not a build attestation")
            continue
        require(platform.get("os") == "linux", "unexpected operating system in image index")
        actual.append(platform.get("architecture"))
    require(Counter(actual) == Counter(architectures), "image index is missing, duplicating or adding a platform")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kind", choices=("qualified", "assembled", "platforms"), required=True)
    parser.add_argument("--directory", type=Path)
    parser.add_argument("--matrix")
    parser.add_argument("--repository")
    parser.add_argument("--revision")
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--architectures", nargs="+")
    args = parser.parse_args()
    if args.kind == "platforms":
        verify_platforms(json.loads(args.manifest.read_text()), args.architectures)
        return
    function = qualified_records if args.kind == "qualified" else assembled_records
    print(json.dumps(function(args.directory, json.loads(args.matrix), args.repository, args.revision)))


if __name__ == "__main__":
    main()
