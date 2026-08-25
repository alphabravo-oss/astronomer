import hashlib
import json
import subprocess
import tempfile
import unittest
from pathlib import Path


class ScaleEvidenceTest(unittest.TestCase):
    def test_manifest_and_baseline_are_deterministic_and_digest_bound(self):
        repo = Path(__file__).resolve().parents[2]
        script = repo / "scripts" / "build-scale-evidence.py"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            profile = "estate-100"
            report = {
                "schema_version": "astronomer-scale-report-v2",
                "generated_at": "2026-08-25T00:00:00Z",
                "profile": profile,
                "verdict": "pass",
                "target_rps": 50,
                "metadata": {"commit": "abc", "images": "sha256:def", "environment": "test", "run_id": "7"},
                "traffic": {"requests": 50, "observed_rps": 50, "observed_duration_seconds": 1},
                "resource_cardinality": {"pods": 1000},
                "state_events_emitted": 10,
                "mandatory_audit": {"accepted": 1, "canonical_rows": 1, "duplicates": 0, "lost": 0},
            }
            (root / f"{profile}.md").write_text("VERDICT: pass\n", encoding="utf-8")
            (root / f"{profile}.md.json").write_text(json.dumps(report), encoding="utf-8")
            (root / f"{profile}.md.sha256").write_text("digest  report\n", encoding="utf-8")
            (root / "rendered-values.yaml").write_text("replicas: 3\n", encoding="utf-8")
            (root / "drills").mkdir()
            (root / "drills" / "postgres.json").write_text("{}\n", encoding="utf-8")
            (root / "raw").mkdir()
            (root / "raw" / "qualification.json").write_text("{}\n", encoding="utf-8")
            command = [str(script), "--evidence-dir", str(root), "--profile", profile]
            subprocess.run(command, check=True)
            first = (root / "evidence-manifest.json").read_bytes()
            first_row = (root / "baseline-row.json").read_bytes()
            subprocess.run(command, check=True)
            self.assertEqual(first, (root / "evidence-manifest.json").read_bytes())
            self.assertEqual(first_row, (root / "baseline-row.json").read_bytes())
            manifest = json.loads(first)
            values = next(item for item in manifest["files"] if item["path"] == "rendered-values.yaml")
            self.assertEqual(values["sha256"], hashlib.sha256(b"replicas: 3\n").hexdigest())

    def test_aggregate_requires_complete_same_release_set(self):
        repo = Path(__file__).resolve().parents[2]
        script = repo / "scripts" / "aggregate-scale-evidence.py"
        profiles = ["estate-100", "estate-500", "estate-1000", "estate-1000-soak"]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for profile in profiles:
                rung = root / profile
                rung.mkdir()
                manifest = {"profile": profile, "verdict": "pass", "release": {"commit": "abc", "images": "sha256:def", "environment": "prod"}, "provenance": {"run_id": profile}}
                (rung / "evidence-manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
                (rung / "evidence-manifest.sigstore.json").write_text("{}", encoding="utf-8")
                (rung / "baseline-row.json").write_text(json.dumps({"profile": profile}), encoding="utf-8")
            output = root / "set.json"
            sizing = root / "sizing.json"
            sizing.write_text(json.dumps({
                "schema_version": "astronomer-component-sizing-recommendations-v1",
                "release": {"commit": "abc", "images": "sha256:def", "environment": "prod"},
                "status": "evidence-derived-unpublished",
            }), encoding="utf-8")
            sizing_table = root / "sizing.md"
            sizing_table.write_text("| sizing |\n", encoding="utf-8")
            command = [str(script), "--input-dir", str(root), "--sizing", str(sizing), "--sizing-docs-table", str(sizing_table), "--target-version", "v1.2.3", "--source-commit", "a" * 40, "--source-run-id", "123", "--out", str(output)]
            subprocess.run(command, check=True)
            aggregate = json.loads(output.read_text(encoding="utf-8"))
            self.assertEqual([row["profile"] for row in aggregate["profiles"]], sorted(profiles))
            changed = root / profiles[-1] / "evidence-manifest.json"
            manifest = json.loads(changed.read_text(encoding="utf-8"))
            manifest["release"]["commit"] = "different"
            changed.write_text(json.dumps(manifest), encoding="utf-8")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)

    def test_component_sizing_reducer_is_deterministic_and_fail_closed(self):
        repo = Path(__file__).resolve().parents[2]
        script = repo / "scripts" / "reduce-component-sizing.py"
        release = {
            "commit": "abc", "images": "sha256:def", "chart_values": "sha256:123",
            "environment": "prod", "kubernetes_version": "1.31", "postgres_version": "17",
            "redis_version": "7", "hardware": "test-shape",
        }

        def report(profile, rate_factor, path):
            replicas = {"audit": 3, "server": 3, "tunnel": 3, "worker": 3}
            units = {
                "audit": "canonical_audit_rows_per_second", "server": "http_requests_per_second",
                "tunnel": "state_updates_per_second", "worker": "jobs_per_second",
            }
            rates = {"audit": 5, "server": 1000, "tunnel": 5000, "worker": 100}
            body = {
                "schema_version": "astronomer-scale-report-v2", "verdict": "pass", "certification": True,
                "profile": profile, "clusters": int(rate_factor * 100), "target_rps": int(rate_factor * 1000),
                "metadata": {**release, "run_id": f"run-{profile}", "component_replicas": json.dumps(replicas)},
                "component_metrics": {
                    name: {
                        "observed_rate": value * rate_factor, "declared_load": rate_factor * 10,
                        "rate_unit": units[name], "sample_count": 10,
                    }
                    for name, value in rates.items()
                },
            }
            path.write_text(json.dumps(body), encoding="utf-8")

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            first_report, second_report, third_report = root / "a.json", root / "b.json", root / "c.json"
            report("estate-100", 1, first_report)
            report("estate-500", 2, second_report)
            report("estate-1000", 3, third_report)
            output, table = root / "sizing.json", root / "table.md"
            command = [
                str(script), "--report", str(first_report), "--report", str(second_report), "--report", str(third_report),
                "--out", str(output), "--docs-table-out", str(table),
            ]
            subprocess.run(command, check=True)
            first, first_table = output.read_bytes(), table.read_bytes()
            subprocess.run(command, check=True)
            self.assertEqual(first, output.read_bytes())
            self.assertEqual(first_table, table.read_bytes())
            reduced = json.loads(first)
            self.assertEqual(reduced["status"], "evidence-derived-unpublished")
            self.assertEqual(len(reduced["recommendations"]), 3)
            for calibration in reduced["calibration"].values():
                self.assertLessEqual(calibration["conservative_safe_rate_per_replica"], min(calibration["per_report_rates"]))
            self.assertIn("unpublished evidence input", first_table.decode())

            invalid = json.loads(second_report.read_text(encoding="utf-8"))
            del invalid["component_metrics"]["worker"]
            second_report.write_text(json.dumps(invalid), encoding="utf-8")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)
            report("estate-500", 2, second_report)
            invalid = json.loads(second_report.read_text(encoding="utf-8"))
            invalid["component_metrics"]["worker"]["observed_rate"] = 0
            second_report.write_text(json.dumps(invalid), encoding="utf-8")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)

            report("estate-500", 2, second_report)
            invalid = json.loads(second_report.read_text(encoding="utf-8"))
            invalid["metadata"]["run_id"] = "run-estate-100"
            second_report.write_text(json.dumps(invalid), encoding="utf-8")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)

            report("estate-500", 2, second_report)
            invalid = json.loads(second_report.read_text(encoding="utf-8"))
            invalid["component_metrics"]["server"]["observed_rate"] = 500
            second_report.write_text(json.dumps(invalid), encoding="utf-8")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)

            report("estate-500", 2, second_report)
            invalid = json.loads(second_report.read_text(encoding="utf-8"))
            invalid["component_metrics"]["server"]["observed_rate"] = 4000
            second_report.write_text(json.dumps(invalid), encoding="utf-8")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)

            report("estate-500", 2, second_report)
            invalid = json.loads(second_report.read_text(encoding="utf-8"))
            replicas = json.loads(invalid["metadata"]["component_replicas"])
            replicas["server"] = 6
            invalid["metadata"]["component_replicas"] = json.dumps(replicas)
            second_report.write_text(json.dumps(invalid), encoding="utf-8")
            self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
