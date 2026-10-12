import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
from types import SimpleNamespace
from urllib.parse import urlsplit

SPEC = importlib.util.spec_from_file_location("allocator", Path(__file__).parents[1] / "metrics-postgres-allocate.py")
allocator = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(allocator)


class MetricsAllocationTests(unittest.TestCase):
    def test_destination_rejects_dangling_link_and_nonprivate_parent(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.object(allocator, "PRIVATE_ROOT", Path(directory)):
            parent = Path(directory)
            path = parent / "postgres18-database.json"
            parent.chmod(0o700)
            self.assertEqual(allocator.reference_destination(path, "18"), path)
            path.symlink_to(parent / "absent-secret.json")
            with self.assertRaises(ValueError):
                allocator.reference_destination(path, "18")
            path.unlink()
            parent.chmod(0o755)
            with self.assertRaises(ValueError):
                allocator.reference_destination(path, "18")

    def test_destination_rejects_symlink_parent_or_wrong_lane(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            private = root / "private"
            private.mkdir(mode=0o700)
            alias = root / "alias"
            alias.symlink_to(private, target_is_directory=True)
            with mock.patch.object(allocator, "PRIVATE_ROOT", alias):
                with self.assertRaises(ValueError):
                    allocator.reference_destination(alias / "postgres18-database.json", "18")
            with mock.patch.object(allocator, "PRIVATE_ROOT", private):
                with self.assertRaises(ValueError):
                    allocator.reference_destination(private / "postgres17-database.json", "18")

    def test_reference_creation_exclusive0600_and_preserves_partial_on_error(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "reference.json"
            prior_umask = os.umask(0)
            try:
                allocator.write_reference(path, {"synthetic": "value"})
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
                original = path.read_bytes()
                with self.assertRaises(FileExistsError):
                    allocator.write_reference(path, {})
                self.assertEqual(path.read_bytes(), original)
                path.unlink()
                with mock.patch.object(allocator.json, "dumps", side_effect=ValueError("synthetic failure")):
                    with self.assertRaises(ValueError):
                        allocator.write_reference(path, {})
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            finally:
                os.umask(prior_umask)

    def test_template_reference_binds_private_exact_lane(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "template.json"
            valid = "postgres://192.0.2.1:30433/plan032_w07_cursor17?sslmode=disable"
            path.write_text(json.dumps({"database_url": valid}))
            path.chmod(0o600)
            self.assertEqual(allocator.template_reference(path, "17")[1], "plan032_w07_cursor17")
            for url in [valid.replace("cursor17", "cursor18"), valid + "#fragment", valid + "&options=unsafe", valid + "&sslmode=require"]:
                path.write_text(json.dumps({"database_url": url}))
                with self.assertRaises(ValueError):
                    allocator.template_reference(path, "17")
            path.chmod(0o644)
            with self.assertRaises(ValueError):
                allocator.template_reference(path, "17")

    def test_destination_absence_template_connections_and_owner_required(self):
        args = SimpleNamespace(major="18")
        report = {"currentPrimary": "live-primary"}
        template = "plan032_w07_cursor18"
        for last in ["1|0|astronomer", "0|1|astronomer", "0|0|other"]:
            with mock.patch.object(allocator.metrics, "owned_endpoint", return_value=report), mock.patch.object(allocator, "sql", side_effect=[template + "|18|75|f", last]):
                with self.assertRaises(ValueError):
                    allocator.inspect(args, urlsplit("postgres://192.0.2.1:30433/" + template), template)

    def test_actual_major_schema_and_dirty_rejected_before_create(self):
        args = SimpleNamespace(major="18")
        template = "plan032_w07_cursor18"
        for last in [template + "|17|75|f", template + "|18|74|f", template + "|18|75|t"]:
            with mock.patch.object(allocator.metrics, "owned_endpoint", return_value={"currentPrimary": "live-primary"}), mock.patch.object(allocator, "sql", return_value=last) as sql:
                with self.assertRaises(ValueError):
                    allocator.inspect(args, urlsplit("postgres://192.0.2.1:30433/" + template), template)
                self.assertEqual(sql.call_count, 1)

    def test_inspection_uses_current_primary_and_only_selects(self):
        args = SimpleNamespace(major="17")
        template = "plan032_w07_cursor17"
        report = {"currentPrimary": "new-live-primary"}
        with mock.patch.object(allocator.metrics, "owned_endpoint", return_value=report), mock.patch.object(allocator, "sql", side_effect=[template + "|17|75|f", "0|0|astronomer"]) as sql:
            self.assertEqual(allocator.inspect(args, urlsplit("postgres://192.0.2.1:30433/" + template), template), report)
            for call in sql.call_args_list:
                self.assertEqual(call.args[1], "new-live-primary")
                self.assertTrue(call.args[3].startswith("SELECT "))

    def test_final_capacity_retained_after_inspection_error(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            args = SimpleNamespace(major="18", reference=str(root / "reference"), template_reference="private", artifact_dir=str(root / "evidence"), create=False)
            address = urlsplit("postgres://192.0.2.1:30433/plan032_w07_cursor18")
            process = SimpleNamespace(returncode=0, stdout='{"meets_minimum":true}')
            with mock.patch.object(allocator, "reference_destination", return_value=root / "reference"), mock.patch.object(allocator, "template_reference", return_value=(address, "plan032_w07_cursor18", "digest")), mock.patch.object(allocator, "inspect", side_effect=ValueError("private secret text")), mock.patch.object(allocator.subprocess, "run", return_value=process) as run:
                self.assertEqual(allocator.allocate(args), 1)
            self.assertEqual(run.call_count, 2)
            receipt = json.loads((root / "evidence" / "receipt.json").read_text())
            self.assertFalse(receipt["created"])
            self.assertEqual(receipt["finalCapacityExitCode"], 0)
            self.assertTrue((root / "evidence" / "capacity-after.json").is_file())
            self.assertNotIn("private secret text", json.dumps(receipt))

    def test_final_capacity_failure_fails_successful_read_only_inspection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            args = SimpleNamespace(major="18", reference=str(root / "reference"), template_reference="private", artifact_dir=str(root / "evidence"), create=False)
            address = urlsplit("postgres://192.0.2.1:30433/plan032_w07_cursor18")
            processes = [SimpleNamespace(returncode=0, stdout='{}'), SimpleNamespace(returncode=1, stdout='{}')]
            with mock.patch.object(allocator, "reference_destination", return_value=root / "reference"), mock.patch.object(allocator, "template_reference", return_value=(address, "plan032_w07_cursor18", "digest")), mock.patch.object(allocator, "inspect", return_value={"currentPrimary": "owned"}), mock.patch.object(allocator.subprocess, "run", side_effect=processes):
                self.assertEqual(allocator.allocate(args), 1)
            receipt = json.loads((root / "evidence" / "receipt.json").read_text())
            self.assertFalse(receipt["created"])
            self.assertEqual(receipt["errorKind"], "final_capacity_failure")
            self.assertFalse((root / "reference").exists())


if __name__ == "__main__":
    unittest.main()
