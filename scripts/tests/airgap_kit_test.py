#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
KIT_SCRIPT = ROOT / "scripts/airgap-kit.py"
MIRROR_SCRIPT = ROOT / "scripts/mirror-release.py"


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


KIT = load_module("airgap_kit", KIT_SCRIPT)
MIRROR = load_module("mirror_release_for_airgap", MIRROR_SCRIPT)

RAW = "{}"
DIGEST = "sha256:" + hashlib.sha256(RAW.encode()).hexdigest()


def subject(name: str, kind: str) -> dict:
    reference = f"source.example.test/team/{name}@{DIGEST}"
    return {
        "name": name,
        "kind": kind,
        "reference": reference,
        "content_digest": DIGEST,
        "evidence": {
            "signature": f"cosign://{reference}",
            "sbom": f"cosign-attestation://{reference}#spdxjson",
            "provenance": f"cosign-attestation://{reference}#slsaprovenance",
        },
    }


def manifest() -> dict:
    names = ("agent", "frontend", "migrate", "server", "shell", "worker")
    images = [subject(name, "container_image") for name in names]
    return {
        "schema_version": 1,
        "release": {
            "version": "v1.0.0",
            "artifact_signing_policy": {
                "certificate_identity": "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/v1.0.0",
                "certificate_oidc_issuer": "https://token.actions.githubusercontent.com",
            },
        },
        "astronomer": {
            "chart": subject("chart", "helm_chart"),
            "images": images,
            "runtime_images": [
                {"source_reference": source, "reference": exact}
                for source, exact in {
                    "busybox:1.36": f"docker.io/library/busybox@{DIGEST}",
                    "postgres:16-alpine": f"docker.io/library/postgres@{DIGEST}",
                    "valkey/valkey:8-alpine": f"docker.io/valkey/valkey@{DIGEST}",
                    "dexidp/dex:v2.41.1": f"docker.io/dexidp/dex@{DIGEST}",
                    "fluent/fluent-bit:3.2.4": f"docker.io/fluent/fluent-bit@{DIGEST}",
                    "ghcr.io/alphabravocompany/pgdump-s3:16-awscli": f"ghcr.io/alphabravocompany/pgdump-s3@{DIGEST}",
                }.items()
            ],
        },
        "flux": {
            "distribution": subject("flux", "oci_artifact"),
            "controllers": [subject("source-controller", "container_image")],
        },
        "built_in_bundles": {
            "artifact": subject("bundles", "oci_artifact"),
            "components": [{"images": [f"images.example.test/metrics/exporter@{DIGEST}"]}],
        },
        "charlie": {"artifact": subject("charlie", "container_image")},
    }


class AirgapKitTest(unittest.TestCase):
    def test_images_txt_is_complete_sorted_and_digest_pinned(self) -> None:
        listing = KIT.images_txt(manifest())
        lines = [line for line in listing.splitlines() if line and not line.startswith("#")]
        self.assertEqual(lines, sorted(lines))
        self.assertTrue(all("@sha256:" in line for line in lines))
        self.assertIn(f"source.example.test/team/server@{DIGEST}", lines)
        self.assertIn(f"docker.io/library/busybox@{DIGEST}", lines)
        self.assertIn(f"images.example.test/metrics/exporter@{DIGEST}", lines)
        self.assertIn(f"source.example.test/team/charlie@{DIGEST}", lines)
        self.assertNotIn(f"source.example.test/team/chart@{DIGEST}", lines)
        first_party = KIT.images_txt(manifest(), first_party=True)
        first_lines = [line for line in first_party.splitlines() if line and not line.startswith("#")]
        self.assertEqual(len(first_lines), 6)
        self.assertTrue(all(line.startswith("source.example.test/team/") for line in first_lines))

    def test_destination_rewrite_matches_mirror_plan(self) -> None:
        document = manifest()
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "release.json"
            path.write_bytes(MIRROR.canonical(document))
            mapping = MIRROR.plan(path, "mirror.example.test:5000")
        by_source = {entry["source"]: entry["target"] for entry in mapping["entries"] if entry["kind"] == "container_image"}
        for source, target in by_source.items():
            self.assertEqual(KIT.destination_for(source, "mirror.example.test:5000"), target)

    def test_pack_is_deterministic_and_self_contained(self) -> None:
        document = manifest()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            manifest_path = root / "release-manifest.json"
            manifest_path.write_bytes(MIRROR.canonical(document))
            chart = root / "astronomer-1.0.0.tgz"
            chart.write_bytes(b"chart")
            values = root / "values-production.yaml"
            values.write_text("image: {}\n", encoding="utf-8")
            signature = root / "release-manifest.sigstore.json"
            signature.write_text("{}", encoding="utf-8")
            flux = root / "flux-distribution.tar.gz"
            flux.write_bytes(b"flux")
            bundles = root / "builtin-bundles.tar.gz"
            bundles.write_bytes(b"bundles")
            first = root / "a.tar.gz"
            second = root / "b.tar.gz"
            KIT.pack(
                manifest_path=manifest_path,
                chart_package=chart,
                values_production=values,
                output=first,
                signature=signature,
                flux_archive=flux,
                bundles_archive=bundles,
            )
            KIT.pack(
                manifest_path=manifest_path,
                chart_package=chart,
                values_production=values,
                output=second,
                signature=signature,
                flux_archive=flux,
                bundles_archive=bundles,
            )
            self.assertEqual(first.read_bytes(), second.read_bytes())
            names = set()
            with tarfile.open(first, mode="r:gz") as archive:
                names = set(archive.getnames())
            prefix = "astronomer-airgap-v1.0.0/"
            for required in (
                "README.md",
                "SHA256SUMS",
                "astronomer-images.txt",
                "astronomer-save-images.sh",
                "astronomer-load-images.sh",
                "airgap-kit.py",
                "mirror-release.py",
                "release-manifest.json",
                "release-manifest.sigstore.json",
                "astronomer-1.0.0.tgz",
                "values-production.yaml",
                "flux-distribution.tar.gz",
                "builtin-bundles.tar.gz",
            ):
                self.assertIn(prefix + required, names)

    def test_save_and_load_use_skopeo_without_credential_flags(self) -> None:
        document = manifest()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            manifest_path = root / "release.json"
            manifest_path.write_bytes(MIRROR.canonical(document))
            bin_dir = root / "bin"
            bin_dir.mkdir()
            log = root / "skopeo.log"
            skopeo = bin_dir / "skopeo"
            skopeo.write_text(
                """#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$*" >>"$SKOPEO_LOG"
if [[ "$1" != "copy" ]]; then exit 2; fi
dest="${@: -1}"
src="${@: -2:1}"
if [[ "$dest" == dir:* ]]; then
  mkdir -p "${dest#dir:}"
  printf '%s' "$src" >"${dest#dir:}/from"
elif [[ "$dest" == docker://* ]]; then
  dir="${src#dir:}"
  test -d "$dir"
  printf '%s' "$dest" >"$dir/pushed"
else exit 2; fi
""",
                encoding="utf-8",
            )
            skopeo.chmod(skopeo.stat().st_mode | stat.S_IXUSR)
            env = os.environ.copy()
            env["PATH"] = f"{bin_dir}{os.pathsep}{env.get('PATH', '')}"
            env["SKOPEO_LOG"] = str(log)
            old = os.environ.copy()
            os.environ.clear()
            os.environ.update(env)
            try:
                archive = root / "images.tar.gz"
                KIT.save(manifest_path=manifest_path, output=archive, first_party=True)
                values = root / "values.json"
                KIT.load(
                    manifest_path=manifest_path,
                    images_archive=archive,
                    destination_registry="mirror.example.test",
                    values_output=values,
                )
            finally:
                os.environ.clear()
                os.environ.update(old)
            logged = log.read_text(encoding="utf-8")
            self.assertIn("--preserve-digests", logged)
            self.assertIn("--override-arch amd64", logged)
            self.assertNotIn("--all", logged)
            self.assertNotRegex(logged, r"password|token|secret")
            self.assertTrue(values.is_file())
            document_values = json.loads(values.read_text(encoding="utf-8"))
            self.assertEqual(document_values["delivery"]["artifacts"]["privateRegistry"], "mirror.example.test")

    def test_cli_refuses_secret_shaped_arguments(self) -> None:
        self.assertEqual(KIT.main(["list-images", "--manifest", "x", "--password=secret"]), 1)
        self.assertEqual(KIT.main(["load", "--token", "abc"]), 1)


if __name__ == "__main__":
    unittest.main()
