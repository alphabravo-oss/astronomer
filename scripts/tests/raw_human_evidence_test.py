import hashlib
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "scripts" / "raw-human-evidence-manifest.py"
ACCESSIBILITY_BUILDER = ROOT / "scripts" / "build-accessibility-evidence.py"
RANCHER_BUILDER = ROOT / "scripts" / "build-rancher-human-study.mjs"
COMMIT = "b" * 40


class RawHumanEvidenceTest(unittest.TestCase):
    def run_manifest(self, evidence_root: Path, verify: bool = False):
        command = [sys.executable, str(MANIFEST), "--root", str(evidence_root), "--manifest", str(evidence_root / "raw-manifest.json")]
        if verify:
            command.append("--verify")
        return subprocess.run(command, capture_output=True, text=True)

    def test_manifest_survives_signature_bundle_but_rejects_tamper_and_extra_file(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            evidence_root = base / "raw"
            evidence_root.mkdir()
            payload = evidence_root / "retained.txt"
            payload.write_bytes(b"original evidence\n")
            self.assertEqual(self.run_manifest(evidence_root).returncode, 0)
            (evidence_root / "raw-manifest.sigstore.json").write_text('{"signed":true}\n', encoding="utf-8")
            self.assertEqual(self.run_manifest(evidence_root, verify=True).returncode, 0)

            payload.write_bytes(b"tampered evidence\n")
            self.assertNotEqual(self.run_manifest(evidence_root, verify=True).returncode, 0)
            payload.write_bytes(b"original evidence\n")
            (evidence_root / "unlisted.txt").write_text("late file\n", encoding="utf-8")
            self.assertNotEqual(self.run_manifest(evidence_root, verify=True).returncode, 0)

    def test_manifest_rejects_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence_root = Path(directory)
            target = evidence_root / "target.txt"
            target.write_text("evidence\n", encoding="utf-8")
            (evidence_root / "alias.txt").symlink_to(target)
            self.assertNotEqual(self.run_manifest(evidence_root).returncode, 0)

    def test_accessibility_builder_recomputes_retained_file_digest(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            evidence_root = base / "raw"
            evidence_root.mkdir()
            retained = evidence_root / "nvda-session.txt"
            retained.write_bytes(b"observed session\n")
            records = {
                "started_at": "2026-08-25T00:00:00Z",
                "completed_at": "2026-08-25T00:10:00Z",
                "reviewer": "accessibility-owner",
                "matrix": [{
                    "platform": "windows", "browser": "chrome", "assistive_technology": "NVDA",
                    "at_version": "2026.1", "viewport": "desktop", "zoom_levels": ["100%", "200%"],
                    "critical_workflows": ["login"], "status": "passed", "open_blocking_defects": 0,
                    "evidence_file": retained.name,
                }],
            }
            (evidence_root / "accessibility-records.json").write_text(json.dumps(records), encoding="utf-8")
            (evidence_root / "raw-manifest.json").write_text("{}\n", encoding="utf-8")
            (evidence_root / "raw-manifest.sigstore.json").write_text("{}\n", encoding="utf-8")
            output = base / "accessibility.json"
            result = subprocess.run([
                sys.executable, str(ACCESSIBILITY_BUILDER), "--raw-dir", str(evidence_root), "--tag", "v1.2.3",
                "--source-commit", COMMIT, "--source-run-id", "123", "--raw-run-id", "99", "--out", str(output),
            ], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            evidence = json.loads(output.read_text(encoding="utf-8"))
            expected = "sha256:" + hashlib.sha256(retained.read_bytes()).hexdigest()
            self.assertEqual(evidence["matrix"][0]["evidence_sha256"], expected)

    def test_rancher_builder_recomputes_results_digest_and_rejects_claimed_digest(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            evidence_root = base / "raw"
            evidence_root.mkdir()
            retained = evidence_root / "study-results.csv"
            retained.write_bytes(b"participant,answer\nredacted,5\n")
            record = {
                "study_version": "counterbalanced-crossover/v1", "participant_count": 8,
                "counterbalanced": True, "reviewer": "research-owner", "responses": [],
                "results_file": retained.name,
            }
            input_path = evidence_root / "human-evaluation.json"
            input_path.write_text(json.dumps(record), encoding="utf-8")
            output = base / "human-output.json"
            command = ["node", str(RANCHER_BUILDER), "--raw-dir", str(evidence_root), "--raw-run-id", "99", "--out", str(output)]
            result = subprocess.run(command, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            built = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual(built["results_sha256"], "sha256:" + hashlib.sha256(retained.read_bytes()).hexdigest())

            output.unlink()
            record["results_sha256"] = "sha256:" + "0" * 64
            input_path.write_text(json.dumps(record), encoding="utf-8")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
