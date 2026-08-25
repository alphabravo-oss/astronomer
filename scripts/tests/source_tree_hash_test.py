import importlib.util
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("source_tree_hash", ROOT / "scripts" / "hash-source-tree.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class SourceTreeHashTest(unittest.TestCase):
    def test_clean_dirty_determinism_and_content_binding(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            (root / ".gitignore").write_text("ignored\n", encoding="utf-8")
            tracked = root / "tracked.txt"
            tracked.write_text("one\n", encoding="utf-8")
            subprocess.run(["git", "add", ".gitignore", "tracked.txt"], cwd=root, check=True)
            first = MODULE.tree_identity(root)
            self.assertEqual(first, MODULE.tree_identity(root))

            tracked.write_text("two\n", encoding="utf-8")
            dirty = MODULE.tree_identity(root)
            self.assertNotEqual(first["source_tree_sha256"], dirty["source_tree_sha256"])
            self.assertEqual(dirty, MODULE.tree_identity(root))

            (root / "untracked.txt").write_text("three\n", encoding="utf-8")
            untracked = MODULE.tree_identity(root)
            self.assertNotEqual(dirty["source_tree_sha256"], untracked["source_tree_sha256"])
            (root / "ignored").write_text("not evidence\n", encoding="utf-8")
            self.assertEqual(untracked, MODULE.tree_identity(root))


if __name__ == "__main__":
    unittest.main()
