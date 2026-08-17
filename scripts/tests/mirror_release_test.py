#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/mirror-release.py"
SPEC = importlib.util.spec_from_file_location("mirror_release", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


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
    image = subject("server", "container_image")
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
            "images": [image],
            "runtime_images": [{"source_reference": "server:v1", "reference": image["reference"]}],
        },
        "flux": {"distribution": subject("flux", "oci_artifact"), "controllers": [subject("source-controller", "container_image")]},
        "built_in_bundles": {
            "artifact": subject("bundles", "oci_artifact"),
            "components": [{"images": [f"images.example.test/metrics/exporter@{DIGEST}"]}],
        },
        "charlie": {"artifact": subject("charlie", "container_image")},
    }


class MirrorReleaseTest(unittest.TestCase):
    def test_plan_is_deterministic_digest_pinned_and_safely_collapses_identical_blobs(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "release.json"
            path.write_text(json.dumps(manifest(), sort_keys=True), encoding="utf-8")
            first = MODULE.plan(path, "mirror.example.test:5000")
            second = MODULE.plan(path, "mirror.example.test:5000")
            self.assertEqual(MODULE.canonical(first), MODULE.canonical(second))
            self.assertTrue(all("@sha256:" in entry["source"] and "@sha256:" in entry["target"] for entry in first["entries"]))
            self.assertEqual(sorted(first["registry_rewrites"].values()), ["mirror.example.test:5000"] * len(first["registry_rewrites"]))

            collision = manifest()
            collision["built_in_bundles"]["components"][0]["images"].append(
                f"other.example.test/team/server@{DIGEST}"
            )
            path.write_text(json.dumps(collision), encoding="utf-8")
            collapsed = MODULE.plan(path, "mirror.example.test:5000")
            duplicate_targets = [
                entry["target"] for entry in collapsed["entries"]
                if entry["source"].endswith(f"/team/server@{DIGEST}")
            ]
            self.assertEqual(len(duplicate_targets), 2)
            self.assertEqual(len(set(duplicate_targets)), 1)

    def test_install_values_pin_every_management_and_delivery_subject(self) -> None:
        document = manifest()
        document["astronomer"]["images"] = [
            subject(name, "container_image")
            for name in ("agent", "frontend", "migrate", "server", "shell", "worker")
        ]
        runtime = {
            "busybox:1.36": f"docker.io/library/busybox@{DIGEST}",
            "postgres:16-alpine": f"docker.io/library/postgres@{DIGEST}",
            "valkey/valkey:8-alpine": f"docker.io/valkey/valkey@{DIGEST}",
            "dexidp/dex:v2.41.1": f"docker.io/dexidp/dex@{DIGEST}",
            "fluent/fluent-bit:3.2.4": f"docker.io/fluent/fluent-bit@{DIGEST}",
            "ghcr.io/alphabravocompany/pgdump-s3:16-awscli": f"ghcr.io/alphabravocompany/pgdump-s3@{DIGEST}",
        }
        document["astronomer"]["runtime_images"] = [
            {"source_reference": source, "reference": exact}
            for source, exact in runtime.items()
        ]
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "release.json"
            path.write_bytes(MODULE.canonical(document))
            mapping = MODULE.plan(path, "mirror.example.test:5000")
            values = MODULE.install_values(document, mapping)
        self.assertEqual(values["image"]["server"]["digest"], DIGEST)
        self.assertEqual(values["image"]["registry"], "")
        self.assertEqual(values["utilities"]["busybox"]["repository"], "library/busybox")
        self.assertEqual(values["managementRestoreDrill"]["sidecar"]["image"]["digest"], DIGEST)
        self.assertEqual(values["delivery"]["artifacts"]["privateRegistry"], "mirror.example.test:5000")
        self.assertEqual(values["delivery"]["artifacts"]["fluxDistribution"]["digest"], DIGEST)
        self.assertIn("@sha256:", values["kubectlShell"]["image"])

    def test_apply_copies_verifies_and_signs_exact_plan(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            manifest_path = root / "release.json"
            manifest_path.write_text(json.dumps(manifest(), sort_keys=True), encoding="utf-8")
            plan_path = root / "mapping.json"
            plan_path.write_bytes(MODULE.canonical(MODULE.plan(manifest_path, "mirror.example.test")))
            state = root / "state"
            log = root / "log"
            bin_dir = root / "bin"
            bin_dir.mkdir()
            self._write_tool(
                bin_dir / "skopeo",
                f"""#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == "inspect" ]]; then
  ref="${{@: -1}}"
  grep -Fxq "$ref" "$MIRROR_STATE" 2>/dev/null || exit 1
  printf '{{}}'
elif [[ "$1" == "copy" ]]; then
  ref="${{@: -1}}"
  repo="${{ref%:sha256-*}}"
  printf '%s@{DIGEST}\n' "$repo" >>"$MIRROR_STATE"
  printf 'skopeo %s\n' "$*" >>"$MIRROR_LOG"
else exit 2; fi
""",
            )
            self._write_tool(
                bin_dir / "oras",
                f"""#!/usr/bin/env bash
set -euo pipefail
if [[ "$1 $2" == "manifest fetch" ]]; then
  ref="${{@: -1}}"
  grep -Fxq "$ref" "$MIRROR_STATE" 2>/dev/null || exit 1
  printf '{{"digest":"{DIGEST}"}}'
elif [[ "$1" == "copy" ]]; then
  ref="${{@: -1}}"
  repo="${{ref%:sha256-*}}"
  printf '%s@{DIGEST}\n' "$repo" >>"$MIRROR_STATE"
  printf 'oras %s\n' "$*" >>"$MIRROR_LOG"
else exit 2; fi
""",
            )
            self._write_tool(
                bin_dir / "cosign",
                """#!/usr/bin/env bash
set -euo pipefail
bundle=''
while [[ $# -gt 0 ]]; do
  [[ "$1" == "--bundle" ]] && bundle="$2" && shift 2 || shift
done
printf '{"signed":true}\n' >"$bundle"
printf 'cosign\n' >>"$MIRROR_LOG"
""",
            )
            env = os.environ.copy()
            env.update({"PATH": str(bin_dir) + ":" + env["PATH"], "MIRROR_STATE": str(state), "MIRROR_LOG": str(log)})
            signature = root / "mapping.sigstore.json"
            completed = subprocess.run(
                [str(SCRIPT), "apply", "--plan", str(plan_path), "--signature-output", str(signature)],
                cwd=ROOT,
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            self.assertEqual(completed.returncode, 0, completed.stdout + completed.stderr)
            self.assertTrue(signature.is_file())
            expected_ids = {entry["id"] for entry in json.loads(plan_path.read_text())["entries"]}
            reported = {
                line.split()[2]
                for line in completed.stdout.splitlines()
                if line.startswith("mirror-release: copied ")
            }
            self.assertEqual(reported, expected_ids)
            self.assertIn("cosign", log.read_text(encoding="utf-8"))

    @staticmethod
    def _write_tool(path: Path, contents: str) -> None:
        path.write_text(contents, encoding="utf-8")
        path.chmod(path.stat().st_mode | stat.S_IXUSR)


if __name__ == "__main__":
    unittest.main()
