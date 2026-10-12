import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("kubernetes", Path(__file__).parents[1] / "qualification-kubernetes.py")
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


class SkewTests(unittest.TestCase):
    def version(self, value):
        return {"clientVersion": {"gitVersion": value}}

    def test_selected_client_on_current_and_transition(self):
        for minor in ("1.36", "1.37"):
            self.assertEqual(m.check_client(self.version("v1.37.1"), minor), "v1.37.1")

    def test_both_skew_edges(self):
        for version in ("v1.35.9", "v1.37.1"):
            m.check_client(self.version(version), "1.36")

    def test_host_old_client_is_unsupported(self):
        with self.assertRaisesRegex(ValueError, "within one"):
            m.check_client(self.version("v1.34.9"), "1.36")

    def test_future_too_new_client_is_unsupported(self):
        with self.assertRaisesRegex(ValueError, "within one"):
            m.check_client(self.version("v1.39.0"), "1.37")

    def test_rc_wrong_major_and_missing_metadata_are_not_certified(self):
        for value in ("v1.37.2-rc.1", "v2.36.0", "", "v1.37-malicious"):
            with self.assertRaises(ValueError):
                m.check_client(self.version(value), "1.36")

    def test_undeclared_server_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "only owned"):
            m.check_client(self.version("v1.35.2"), "1.34")


if __name__ == "__main__":
    unittest.main()
