#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
from pathlib import Path
import stat
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("resolve_release_images", ROOT / "scripts/resolve-release-images.py")
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ResolveReleaseImagesTest(unittest.TestCase):
    def test_resolves_tags_and_skips_existing_digests(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            inventory = root / "images.txt"
            inventory.write_text(
                "# header\npostgres:16-alpine\nghcr.io/example/tool:v1\n"
                "quay.io/example/exact@sha256:" + "a" * 64 + "\n",
                encoding="utf-8",
            )
            resolver = root / "resolver"
            resolver.write_text(
                "#!/usr/bin/env bash\nprintf 'Name: %s\\nDigest: sha256:%064d\\n' \"$1\" 1\n",
                encoding="utf-8",
            )
            resolver.chmod(resolver.stat().st_mode | stat.S_IXUSR)
            result = MODULE.resolve(inventory, [str(resolver)])
            self.assertEqual(
                result["postgres:16-alpine"],
                "docker.io/library/postgres@sha256:" + "0" * 63 + "1",
            )
            self.assertEqual(
                result["ghcr.io/example/tool:v1"],
                "ghcr.io/example/tool@sha256:" + "0" * 63 + "1",
            )
            self.assertEqual(len(result), 2)


if __name__ == "__main__":
    unittest.main()
