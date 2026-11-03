import copy
import json
from pathlib import Path
import tempfile
import unittest

from image_platforms import assembled_records, qualified_records, verify_platforms


class PlatformEvidenceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name)
        self.repo, self.revision = "ghcr.io/example/jotist", "a" * 40
        self.matrix = {"include": [{"variant": "cpu", "architecture": arch, "suffix": ""} for arch in ("amd64", "arm64")]}
        for i, row in enumerate(self.matrix["include"]):
            self.write("qualified-cpu-" + row["architecture"], {**row, "repository": self.repo, "revision": self.revision, "digest": "sha256:" + str(i) * 64})

    def write(self, name, record):
        (self.path / (name + ".json")).write_text(json.dumps(record))

    def records(self, matrix=None):
        return qualified_records(self.path, matrix or self.matrix, self.repo, self.revision)

    def test_both_cpu_platforms_are_required_even_when_one_passes(self):
        self.assertEqual(len(self.records()), 2)
        matrix = copy.deepcopy(self.matrix)
        matrix["include"].pop()
        with self.assertRaisesRegex(ValueError, "missing"):
            self.records(matrix)
        (self.path / "qualified-cpu-arm64.json").unlink()
        with self.assertRaises(FileNotFoundError):
            self.records()

    def test_cross_revision_or_cross_architecture_proof_is_rejected(self):
        file = self.path / "qualified-cpu-arm64.json"
        original = json.loads(file.read_text())
        for key, value in (("architecture", "amd64"), ("revision", "b" * 40), ("repository", "ghcr.io/other/jotist")):
            with self.subTest(key=key):
                self.write("qualified-cpu-arm64", {**original, key: value})
                with self.assertRaisesRegex(ValueError, "identity"):
                    self.records()

    def test_assembly_must_retain_exact_qualified_source_digests(self):
        leaves = self.records()
        record = {"repository": self.repo, "revision": self.revision, "variant": "cpu", "suffix": "", "digest": "sha256:" + "c" * 64,
                  "sources": {r["architecture"]: r["digest"] for r in leaves}}
        self.write("assembled-cpu", record)
        self.assertEqual(len(assembled_records(self.path, self.matrix, self.repo, self.revision)), 1)
        record["sources"]["arm64"] = "sha256:" + "d" * 64
        self.write("assembled-cpu", record)
        with self.assertRaisesRegex(ValueError, "source images"):
            assembled_records(self.path, self.matrix, self.repo, self.revision)

    def test_index_rejects_missing_duplicate_and_extra_platforms(self):
        manifest = {"manifests": [{"platform": {"architecture": arch, "os": "linux"}} for arch in ("amd64", "arm64")]}
        verify_platforms(manifest, ["amd64", "arm64"])
        for architectures in (("amd64",), ("amd64", "amd64"), ("amd64", "arm64", "arm") ):
            wrong = {"manifests": [{"platform": {"architecture": arch, "os": "linux"}} for arch in architectures]}
            with self.assertRaises(ValueError):
                verify_platforms(wrong, ["amd64", "arm64"])

    def test_unknown_platform_is_allowed_only_for_build_attestations(self):
        manifest = {"manifests": [{"platform": {"architecture": "arm64", "os": "linux"}},
                                  {"platform": {"architecture": "unknown", "os": "unknown"}, "annotations": {"vnd.docker.reference.type": "attestation-manifest"}}]}
        verify_platforms(manifest, ["arm64"])
        manifest["manifests"][1].pop("annotations")
        with self.assertRaisesRegex(ValueError, "attestation"):
            verify_platforms(manifest, ["arm64"])


if __name__ == "__main__":
    unittest.main()
