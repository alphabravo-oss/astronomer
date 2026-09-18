#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import importlib.util
import io
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
    names = ("agent", "dr", "frontend", "migrate", "server", "shell", "worker")
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
    def write_minimal_image_archive(
        self,
        path: Path,
        manifest_path: Path,
        document: dict,
        *,
        index_mutator=None,
        extra_members: list[tuple[tarfile.TarInfo, bytes]] | None = None,
        payload: bytes = b"immutable-image-layout",
    ) -> None:
        directory = DIGEST.removeprefix("sha256:")
        payload_name = f"{directory}/manifest.json"
        sources = KIT.container_images(document, first_party=True)
        index = {
            "schema_version": 1,
            "release_version": document["release"]["version"],
            "release_manifest_sha256": KIT.sha256_path(manifest_path),
            "scope": "first_party",
            "os": "linux",
            "arch": "amd64",
            "all_platforms": False,
            "images": [{"source": source, "digest": DIGEST, "dir": directory} for source in sources],
            "members": {payload_name: hashlib.sha256(payload).hexdigest()},
        }
        if index_mutator is not None:
            index_mutator(index)
        with tarfile.open(path, mode="w:gz") as archive:
            index_bytes = KIT.canonical(index)
            info = tarfile.TarInfo("index.json")
            info.size = len(index_bytes)
            archive.addfile(info, io.BytesIO(index_bytes))
            directory_info = tarfile.TarInfo(directory)
            directory_info.type = tarfile.DIRTYPE
            archive.addfile(directory_info)
            payload_info = tarfile.TarInfo(payload_name)
            payload_info.size = len(payload)
            archive.addfile(payload_info, io.BytesIO(payload))
            for member, data in extra_members or []:
                member.size = len(data) if member.isreg() else 0
                archive.addfile(member, io.BytesIO(data) if member.isreg() else None)

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
        self.assertEqual(len(first_lines), 7)
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
            cosign = bin_dir / "cosign"
            cosign.write_text(
                """#!/usr/bin/env bash
set -euo pipefail
test "$1" = verify-blob
printf '%s\n' "$*" >>"$SKOPEO_LOG"
""",
                encoding="utf-8",
            )
            cosign.chmod(cosign.stat().st_mode | stat.S_IXUSR)
            signature = root / "release.sigstore.json"
            signature.write_text("{}", encoding="utf-8")
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
                    signature=signature,
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
            self.assertIn("verify-blob --bundle", logged)
            self.assertIn("release.yaml@refs/tags/v1.0.0", logged)
            self.assertNotIn("--all", logged)
            self.assertNotRegex(logged, r"password|secret|--token(?:=|\s)")
            self.assertTrue(values.is_file())
            document_values = json.loads(values.read_text(encoding="utf-8"))
            self.assertEqual(document_values["delivery"]["artifacts"]["privateRegistry"], "mirror.example.test")

    def test_cli_refuses_secret_shaped_arguments(self) -> None:
        self.assertEqual(KIT.main(["list-images", "--manifest", "x", "--password=secret"]), 1)
        self.assertEqual(KIT.main(["load", "--token", "abc"]), 1)

    def test_load_rejects_malicious_archive_before_extraction(self) -> None:
        document = manifest()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            manifest_path = root / "release.json"
            manifest_path.write_bytes(MIRROR.canonical(document))

            cases = []
            cases.append(("wrong-release", lambda index: index.__setitem__("release_version", "v9.9.9"), [], b"immutable-image-layout", "different release"))
            cases.append(("open-schema", lambda index: index.__setitem__("unexpected", True), [], b"immutable-image-layout", "closed v1"))
            link = tarfile.TarInfo("link")
            link.type = tarfile.SYMTYPE
            link.linkname = "/etc/passwd"
            cases.append(("symlink", None, [(link, b"")], b"immutable-image-layout", "regular file or directory"))
            traversal = tarfile.TarInfo("../escape")
            cases.append(("traversal", None, [(traversal, b"bad")], b"immutable-image-layout", "unsafe member path"))
            extra = tarfile.TarInfo(f"{DIGEST.removeprefix('sha256:')}/extra")
            cases.append(("extra-payload", None, [(extra, b"bad")], b"immutable-image-layout", "member set"))
            duplicate = tarfile.TarInfo(f"{DIGEST.removeprefix('sha256:')}/manifest.json")
            cases.append(("duplicate", None, [(duplicate, b"again")], b"immutable-image-layout", "duplicate member"))
            cases.append(("checksum", None, [], b"tampered", "checksum mismatch"))

            for name, mutate, extras, payload, message in cases:
                with self.subTest(name=name):
                    archive_path = root / f"{name}.tar.gz"
                    self.write_minimal_image_archive(
                        archive_path,
                        manifest_path,
                        document,
                        index_mutator=mutate,
                        extra_members=extras,
                        payload=payload,
                    )
                    if name == "checksum":
                        # Keep the authenticated inventory on the original bytes.
                        def original_checksum(index):
                            key = next(iter(index["members"]))
                            index["members"][key] = hashlib.sha256(b"immutable-image-layout").hexdigest()

                        self.write_minimal_image_archive(
                            archive_path,
                            manifest_path,
                            document,
                            index_mutator=original_checksum,
                            payload=payload,
                        )
                    with tarfile.open(archive_path, mode="r:gz") as archive:
                        with self.assertRaisesRegex(KIT.KitError, message):
                            KIT.validate_image_archive(archive, document, manifest_path)


if __name__ == "__main__":
    unittest.main()
