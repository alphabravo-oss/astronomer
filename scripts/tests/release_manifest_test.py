#!/usr/bin/env python3
from __future__ import annotations

import argparse
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


GENERATOR = load_module("generate_release_manifest", ROOT / "scripts/generate-release-manifest.py")
SCHEMA_VALIDATOR = load_module("compatibility_contract_for_release", ROOT / "scripts/compatibility-contract.py")


def digest(character: str) -> str:
    return "sha256:" + character * 64


class ReleaseManifestTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.images = self.root / "images"
        self.images.mkdir()
        for index, (name, repository) in enumerate(sorted(GENERATOR.FIRST_PARTY.items()), start=1):
            hexadecimal = format(index, "x")
            (self.images / f"{name}.digest").write_text(
                f"registry.example.test/astronomer/{repository}@{digest(hexadecimal)}\n",
                encoding="utf-8",
            )
        self.inventory = self.root / "images.txt"
        self.inventory.write_text(
            "\n".join(
                f"registry.example.test/astronomer/{repository}:v1.1.0"
                for repository in sorted(GENERATOR.FIRST_PARTY.values())
            )
            + "\nthird.example.test/database/postgres:16\n",
            encoding="utf-8",
        )
        self.resolved = self.root / "resolved.json"
        self.resolved.write_text(
            json.dumps({"third.example.test/database/postgres:16": f"third.example.test/database/postgres@{digest('a')}"}),
            encoding="utf-8",
        )
        self.chart = self.root / "Chart.yaml"
        self.chart.write_text("version: 1.1.0\nappVersion: \"1.1.0\"\n", encoding="utf-8")
        self.chart_package = self.root / "astronomer-1.1.0.tgz"
        self.chart_package.write_bytes(b"chart")
        self.flux_archive = self.root / "flux.tar.gz"
        self.flux_archive.write_bytes(b"flux")
        self.bundles_archive = self.root / "bundles.tar.gz"
        self.bundles_archive.write_bytes(b"bundles")

    def tearDown(self) -> None:
        self.temp.cleanup()

    def args(self) -> argparse.Namespace:
        return argparse.Namespace(
            version="v1.1.0",
            source_commit="1" * 40,
            source_date_epoch=1_700_000_000,
            chart_reference=f"registry.example.test/charts/astronomer@{digest('b')}",
            chart_package=self.chart_package,
            image_digest_dir=self.images,
            resolved_images=self.resolved,
            flux_reference=f"registry.example.test/artifacts/flux@{digest('c')}",
            flux_archive=self.flux_archive,
            bundles_reference=f"registry.example.test/artifacts/bundles@{digest('d')}",
            bundles_archive=self.bundles_archive,
            charlie_version="v1.0.63",
            charlie_reference=f"registry.example.test/charlie/charlie@{digest('e')}",
            charlie_artifact_kind="container_image",
            charlie_capability_digest=digest("f"),
            charlie_certificate_identity="https://github.com/alphabravo-oss/charlie/.github/workflows/release.yml@refs/tags/v1.0.63",
            charlie_certificate_oidc_issuer="https://token.actions.githubusercontent.com",
            compatibility=ROOT / "deploy/release/compatibility.yaml",
            chart_metadata=self.chart,
            image_inventory=self.inventory,
            flux_provenance=ROOT / "deploy/flux/provenance.json",
            bundle_catalog=ROOT / "deploy/bundles/catalog.json",
        )

    def test_manifest_is_deterministic_complete_and_schema_valid(self) -> None:
        first = GENERATOR.build(self.args())
        second = GENERATOR.build(self.args())
        self.assertEqual(GENERATOR.encode(first), GENERATOR.encode(second))
        schema = SCHEMA_VALIDATOR.load_json(ROOT / "deploy/release/release-manifest.schema.json")
        SCHEMA_VALIDATOR.validate_schema(first, schema, schema)
        self.assertEqual(len(first["astronomer"]["images"]), 6)
        self.assertEqual(len(first["flux"]["controllers"]), 3)
        self.assertEqual(first["charlie"]["qualified_version"], "v1.0.63")
        self.assertTrue(first["charlie"]["artifact_signing_policy"]["certificate_identity"].endswith("v1.0.63"))
        runtime_sources = {item["source_reference"] for item in first["astronomer"]["runtime_images"]}
        self.assertIn("third.example.test/database/postgres:16", runtime_sources)
        self.assertIn(
            "registry.k8s.io/kube-state-metrics/kube-state-metrics@sha256:85108987d044b18a098126732f98602df408888c0f7d456241f5abefb9744bc1",
            runtime_sources,
        )

    def test_unresolved_runtime_tag_fails_closed(self) -> None:
        self.resolved.write_text("{}", encoding="utf-8")
        with self.assertRaisesRegex(GENERATOR.ManifestError, "has no immutable resolution"):
            GENERATOR.build(self.args())

    def test_moved_chart_version_and_mutable_charlie_fail_closed(self) -> None:
        args = self.args()
        args.version = "v1.0.1"
        with self.assertRaisesRegex(GENERATOR.ManifestError, "does not match chart"):
            GENERATOR.build(args)
        args = self.args()
        args.charlie_reference = "registry.example.test/charlie/charlie:v1.0.63"
        with self.assertRaisesRegex(GENERATOR.ManifestError, "not an immutable"):
            GENERATOR.build(args)
        args = self.args()
        args.charlie_certificate_identity = args.charlie_certificate_identity.replace("v1.0.63", "v1.0.62")
        with self.assertRaisesRegex(GENERATOR.ManifestError, "exact tagged GitHub workflow"):
            GENERATOR.build(args)


if __name__ == "__main__":
    unittest.main()
