import importlib.util
import json
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


if __name__ == "__main__":
    unittest.main()
