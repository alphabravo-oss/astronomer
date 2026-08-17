#!/usr/bin/env python3
"""Generate the one canonical, immutable Astronomer release manifest.

All dynamic build outputs enter through explicit files or digest references.
The generator also reconciles the chart image inventory, Flux compatibility
contract, verified Flux provenance, and built-in bundle catalog so release CI
cannot quietly publish mutually inconsistent artifacts.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import re
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
DIGEST_RE = re.compile(r"^sha256:[a-f0-9]{64}$")
REFERENCE_RE = re.compile(
    r"^(?:oci://)?[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?/"
    r"[a-z0-9][a-z0-9._/-]*@sha256:[a-f0-9]{64}$"
)
VERSION_RE = re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+$")
COMMIT_RE = re.compile(r"^[a-f0-9]{40}$")
GITHUB_WORKFLOW_IDENTITY_RE = re.compile(
    r"^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/\.github/workflows/"
    r"[A-Za-z0-9_.-]+\.ya?ml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$"
)

FIRST_PARTY = {
    "server": "astronomer-go-server",
    "worker": "astronomer-go-worker",
    "agent": "astronomer-go-agent",
    "migrate": "astronomer-go-migrate",
    "shell": "astronomer-shell",
    "frontend": "astronomer-frontend",
}


class ManifestError(ValueError):
    """A deterministic release-contract violation."""


def load_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ManifestError(f"cannot read JSON {path}: {exc}") from exc


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as handle:
            while chunk := handle.read(1024 * 1024):
                digest.update(chunk)
    except OSError as exc:
        raise ManifestError(f"cannot hash {path}: {exc}") from exc
    return "sha256:" + digest.hexdigest()


def immutable_reference(value: str, label: str) -> str:
    value = value.strip().removeprefix("oci://")
    if REFERENCE_RE.fullmatch(value) is None:
        raise ManifestError(f"{label} is not an immutable repository@sha256 reference: {value!r}")
    return value


def reference_digest(value: str) -> str:
    return value.rsplit("@", 1)[1]


def evidence(reference: str) -> dict[str, str]:
    return {
        "signature": f"cosign://{reference}",
        "sbom": f"cosign-attestation://{reference}#spdxjson",
        "provenance": f"cosign-attestation://{reference}#slsaprovenance",
    }


def artifact(name: str, kind: str, reference: str, content_digest: str | None = None) -> dict[str, Any]:
    reference = immutable_reference(reference, name)
    content_digest = content_digest or reference_digest(reference)
    if DIGEST_RE.fullmatch(content_digest) is None:
        raise ManifestError(f"{name} content digest is invalid: {content_digest!r}")
    return {
        "name": name,
        "kind": kind,
        "reference": reference,
        "content_digest": content_digest,
        "evidence": evidence(reference),
    }


def chart_versions(path: Path) -> tuple[str, str]:
    version = app_version = ""
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.startswith("version:"):
            version = line.split(":", 1)[1].strip().strip('"')
        elif line.startswith("appVersion:"):
            app_version = line.split(":", 1)[1].strip().strip('"')
    if not version or version != app_version:
        raise ManifestError(f"chart version/appVersion are missing or unequal: {version!r}/{app_version!r}")
    return version, app_version


def first_party_images(directory: Path, version: str) -> tuple[list[dict[str, Any]], dict[str, str]]:
    result: list[dict[str, Any]] = []
    by_repository: dict[str, str] = {}
    for name, repository in sorted(FIRST_PARTY.items()):
        path = directory / f"{name}.digest"
        try:
            reference = immutable_reference(path.read_text(encoding="utf-8").strip(), f"{name} digest file")
        except OSError as exc:
            raise ManifestError(f"missing first-party image identity {path}: {exc}") from exc
        image_path = reference.split("@", 1)[0]
        if image_path.rsplit("/", 1)[-1] != repository:
            raise ManifestError(f"{name} image repository is {image_path!r}, expected {repository!r}")
        result.append(artifact(name, "container_image", reference))
        by_repository[repository] = reference
    if len({item["reference"] for item in result}) != len(FIRST_PARTY):
        raise ManifestError("first-party image identities are not unique")
    return result, by_repository


def runtime_images(
    inventory_path: Path,
    resolved_path: Path,
    first_party: dict[str, str],
    builtin_images: list[str],
) -> list[dict[str, str]]:
    resolved_raw = load_json(resolved_path)
    if not isinstance(resolved_raw, dict) or not all(isinstance(key, str) and isinstance(value, str) for key, value in resolved_raw.items()):
        raise ManifestError("resolved image map must be a JSON object of source reference to immutable reference")

    sources = {
        line.strip()
        for line in inventory_path.read_text(encoding="utf-8").splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    }
    sources.update(builtin_images)
    output: list[dict[str, str]] = []
    for source in sorted(sources):
        exact: str | None = None
        if "@sha256:" in source:
            exact = immutable_reference(source, f"runtime image {source}")
        else:
            repository_path = source.rsplit(":", 1)[0] if ":" in source.rsplit("/", 1)[-1] else source
            repository = repository_path.rsplit("/", 1)[-1]
            if repository in first_party:
                exact = first_party[repository]
            elif source in resolved_raw:
                exact = immutable_reference(resolved_raw[source], f"resolved runtime image {source}")
        if exact is None:
            raise ManifestError(f"runtime image {source!r} has no immutable resolution")
        output.append({"source_reference": source, "reference": exact})
    exact_refs = [item["reference"] for item in output]
    if len(exact_refs) != len(set(exact_refs)):
        duplicates = sorted({value for value in exact_refs if exact_refs.count(value) > 1})
        raise ManifestError(f"runtime image inventory resolves duplicate identities: {duplicates}")
    return output


def build(args: argparse.Namespace) -> dict[str, Any]:
    compatibility = load_json(args.compatibility)
    flux_provenance = load_json(args.flux_provenance)
    catalog = load_json(args.bundle_catalog)
    chart_version, app_version = chart_versions(args.chart_metadata)
    version = "v" + chart_version
    if args.version != version or app_version != chart_version or VERSION_RE.fullmatch(args.version) is None:
        raise ManifestError(f"release version {args.version!r} does not match chart {version!r}")
    if COMMIT_RE.fullmatch(args.source_commit) is None:
        raise ManifestError("source commit must be the full 40-character lowercase Git commit")
    if args.source_date_epoch < 0:
        raise ManifestError("source date epoch must be non-negative")

    flux_version = compatibility["flux"]["distribution_version"]
    if flux_version != flux_provenance["flux_release"]["version"]:
        raise ManifestError("Flux compatibility and provenance versions differ")
    expected_components = {entry["name"]: entry for entry in compatibility["flux"]["components"]}
    provenance_components = {entry["component"]: entry for entry in flux_provenance["controller_images"]}
    if set(expected_components) != set(provenance_components):
        raise ManifestError("Flux compatibility and provenance controller sets differ")

    controllers: list[dict[str, Any]] = []
    apis: set[str] = set()
    for name in sorted(expected_components):
        expected = expected_components[name]
        observed = provenance_components[name]
        if expected["digest"] != observed["digest"] or f"{expected['repository']}:{expected['tag']}" != observed["source_ref"]:
            raise ManifestError(f"Flux controller identity differs for {name}")
        controllers.append(artifact(name, "container_image", f"{expected['repository']}@{expected['digest']}"))
        apis.update(expected["apis"])

    if catalog["release"] != args.version:
        raise ManifestError(f"built-in catalog release {catalog['release']!r} does not match {args.version!r}")
    bundle_components: list[dict[str, Any]] = []
    builtin_images: list[str] = []
    for component in sorted(catalog["components"], key=lambda item: item["slug"]):
        images = sorted(immutable_reference(value, f"built-in image for {component['slug']}") for value in component["images"])
        builtin_images.extend(images)
        bundle_components.append(
            {
                "slug": component["slug"],
                "chart": component["source"]["chart"],
                "chart_version": component["source"]["version"],
                "chart_digest": component["source"]["chart_digest"],
                "images": images,
            }
        )

    images, first_party = first_party_images(args.image_digest_dir, args.version)
    runtime = runtime_images(args.image_inventory, args.resolved_images, first_party, builtin_images)
    protocol_range = compatibility["agent_protocol"]["supported_ranges"]
    kube_range = compatibility["kubernetes"]["supported_ranges"]
    if len(protocol_range) != 1 or len(kube_range) != 1:
        raise ManifestError("release manifest v1 requires one contiguous protocol and Kubernetes range")

    generated_at = dt.datetime.fromtimestamp(args.source_date_epoch, tz=dt.timezone.utc).isoformat().replace("+00:00", "Z")
    document = {
        "schema_version": 1,
        "release": {
            "version": args.version,
            "source_commit": args.source_commit,
            "generated_at": generated_at,
            "install_mode": compatibility["install_mode"],
            "artifact_signing_policy": {
                "certificate_oidc_issuer": "https://token.actions.githubusercontent.com",
                "certificate_identity": (
                    "https://github.com/alphabravo-oss/astronomer/.github/workflows/"
                    f"release.yaml@refs/tags/{args.version}"
                ),
            },
        },
        "compatibility": {
            "kubernetes": {
                "minimum_minor": kube_range[0]["minimum_minor"],
                "maximum_minor": kube_range[0]["maximum_minor"],
            },
            "agent_protocol": {
                "name": compatibility["agent_protocol"]["name"],
                "minimum": protocol_range[0]["minimum"],
                "maximum": protocol_range[0]["maximum"],
            },
            "postgresql": {"supported_majors": compatibility["postgresql"]["supported_majors"]},
            "browsers": compatibility["browsers"],
        },
        "astronomer": {
            "chart": artifact("astronomer-chart", "helm_chart", args.chart_reference, sha256_file(args.chart_package)),
            "images": images,
            "runtime_images": runtime,
        },
        "flux": {
            "version": flux_version,
            "distribution": artifact("flux-distribution", "oci_artifact", args.flux_reference, sha256_file(args.flux_archive)),
            "controllers": controllers,
            "apis": sorted(apis),
        },
        "built_in_bundles": {
            "artifact": artifact("astronomer-platform-bundles", "oci_artifact", args.bundles_reference, sha256_file(args.bundles_archive)),
            "catalog_digest": sha256_file(args.bundle_catalog),
            "components": bundle_components,
        },
        "charlie": {
            "qualified_version": args.charlie_version,
            "artifact": artifact("charlie", args.charlie_artifact_kind, args.charlie_reference),
            "capability_disclosure_digest": args.charlie_capability_digest,
            "artifact_signing_policy": {
                "certificate_oidc_issuer": args.charlie_certificate_oidc_issuer,
                "certificate_identity": args.charlie_certificate_identity,
            },
        },
    }
    if not VERSION_RE.fullmatch(args.charlie_version):
        raise ManifestError("Charlie version must be an exact vX.Y.Z version")
    if DIGEST_RE.fullmatch(args.charlie_capability_digest) is None:
        raise ManifestError("Charlie capability disclosure digest must be a sha256 digest")
    if args.charlie_certificate_oidc_issuer != "https://token.actions.githubusercontent.com":
        raise ManifestError("Charlie certificate issuer must be GitHub Actions OIDC")
    if (
        GITHUB_WORKFLOW_IDENTITY_RE.fullmatch(args.charlie_certificate_identity) is None
        or not args.charlie_certificate_identity.endswith(f"@refs/tags/{args.charlie_version}")
    ):
        raise ManifestError("Charlie certificate identity must be one exact tagged GitHub workflow")
    return document


def encode(document: dict[str, Any]) -> bytes:
    return (json.dumps(document, sort_keys=True, indent=2, ensure_ascii=False) + "\n").encode("utf-8")


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    result.add_argument("--version", required=True)
    result.add_argument("--source-commit", required=True)
    result.add_argument("--source-date-epoch", required=True, type=int)
    result.add_argument("--chart-reference", required=True)
    result.add_argument("--chart-package", required=True, type=Path)
    result.add_argument("--image-digest-dir", required=True, type=Path)
    result.add_argument("--resolved-images", required=True, type=Path)
    result.add_argument("--flux-reference", required=True)
    result.add_argument("--flux-archive", required=True, type=Path)
    result.add_argument("--bundles-reference", required=True)
    result.add_argument("--bundles-archive", required=True, type=Path)
    result.add_argument("--charlie-version", required=True)
    result.add_argument("--charlie-reference", required=True)
    result.add_argument("--charlie-artifact-kind", choices=("container_image", "oci_artifact"), default="container_image")
    result.add_argument("--charlie-capability-digest", required=True)
    result.add_argument("--charlie-certificate-identity", required=True)
    result.add_argument("--charlie-certificate-oidc-issuer", required=True)
    result.add_argument("--output", type=Path)
    result.add_argument("--check", type=Path)
    result.add_argument("--compatibility", type=Path, default=ROOT / "deploy/release/compatibility.yaml")
    result.add_argument("--chart-metadata", type=Path, default=ROOT / "deploy/chart/Chart.yaml")
    result.add_argument("--image-inventory", type=Path, default=ROOT / "deploy/chart/images.txt")
    result.add_argument("--flux-provenance", type=Path, default=ROOT / "deploy/flux/provenance.json")
    result.add_argument("--bundle-catalog", type=Path, default=ROOT / "deploy/bundles/catalog.json")
    return result


def main() -> int:
    args = parser().parse_args()
    try:
        payload = encode(build(args))
        if args.check is not None:
            try:
                existing = args.check.read_bytes()
            except OSError as exc:
                raise ManifestError(f"cannot read release manifest check target {args.check}: {exc}") from exc
            if existing != payload:
                raise ManifestError(f"release manifest is stale or was generated from different artifacts: {args.check}")
        if args.output is not None:
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_bytes(payload)
        if args.output is None and args.check is None:
            sys.stdout.buffer.write(payload)
        return 0
    except ManifestError as exc:
        print(f"generate-release-manifest: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
