#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("qualification_go", Path(__file__).parents[1] / "qualification-go.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ContractTests(unittest.TestCase):
    def events(self, *pairs):
        return [json.dumps({"Action": action, "Test": test, "Package": "pkg"}) for action, test in pairs]

    def test_complete(self):
        self.assertTrue(module.check_events(self.events(("run", "expected"), ("pass", "expected")), ["expected"])["complete"])

    def test_missing_does_not_pass(self):
        self.assertFalse(module.check_events(self.events(("pass", "other")), ["expected"])["complete"])

    def test_unexpected_subtest_skip_does_not_pass(self):
        self.assertFalse(module.check_events(self.events(("skip", "expected/child"), ("pass", "expected")), ["expected"])["complete"])

    def test_package_failure_does_not_pass(self):
        self.assertFalse(module.check_events(self.events(("pass", "expected"), ("fail", None)), ["expected"])["complete"])

    def test_duplicate_terminal_rejected(self):
        with self.assertRaisesRegex(ValueError, "duplicate"):
            module.check_events(self.events(("pass", "expected"), ("pass", "expected")), ["expected"])

    def test_invalid_json_rejected(self):
        with self.assertRaises(ValueError):
            module.check_events(["not JSON"], ["expected"])

    def test_malformed_event_rejected(self):
        with self.assertRaisesRegex(ValueError, "invalid"):
            module.check_events(["{}"], ["expected"])

    def test_private_password_redacted(self):
        from unittest.mock import patch
        with patch.dict(module.os.environ, {"TEST_DATABASE_URL": "postgres://test:private-fixture-secret@127.0.0.1/test"}):
            self.assertEqual(module.redact("error private-fixture-secret"), "error [REDACTED]")

    def test_tags_keep_separate_and_equals_values(self):
        self.assertEqual(module.build_tags(["-tags", "integration fixture", "-tags=fixture,local"]), ["fixture", "integration", "local"])

    def test_tags_missing_value_rejected(self):
        with self.assertRaisesRegex(ValueError, "missing"):
            module.build_tags(["-tags"])


if __name__ == "__main__":
    unittest.main()
