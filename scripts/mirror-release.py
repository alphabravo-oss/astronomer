#!/usr/bin/env python3
"""Plan, copy, verify, and sign one immutable Astronomer air-gap release.

The plan is deterministic and contains only repository@digest identities. Copy
operations use argument arrays (never a shell), preserve multi-platform image
manifests, and verify the destination digest before the signed mapping is
emitted. Registry authentication remains in the tools' normal credential stores
and is never accepted in command-line arguments or written to the mapping.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parent.parent
DIGEST_RE = re.compile(r"^sha256:[a-f0-9]{64}$")
VERSION_RE = re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+$")
REGISTRY_RE = re.compile(r"^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$")
REFERENCE_RE = re.compile(
    r"^(?:oci://)?(?P<registry>[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?)/"
    r"(?P<path>[a-z0-9][a-z0-9._/-]*)@(?P<digest>sha256:[a-f0-9]{64})$"
)


class MirrorError(ValueError):
    """A safe, user-actionable mirror contract violation."""


def load_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise MirrorError(f"cannot read JSON {path}: {exc}") from exc


def canonical(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, indent=2, ensure_ascii=False) + "\n").encode("utf-8")


def file_digest(path: Path) -> str:
    try:
        payload = path.read_bytes()
    except OSError as exc:
        raise MirrorError(f"cannot read {path}: {exc}") from exc
    return "sha256:" + hashlib.sha256(payload).hexdigest()


def add_subject(subjects: dict[str, str], reference: str, kind: str) -> None:
    reference = reference.removeprefix("oci://")
    if REFERENCE_RE.fullmatch(reference) is None:
        raise MirrorError(f"release manifest contains a mutable or invalid reference: {reference!r}")
    previous = subjects.get(reference)
    if previous is not None and previous != kind:
        raise MirrorError(f"release reference {reference!r} has conflicting kinds {previous!r}/{kind!r}")
    subjects[reference] = kind


def add_artifact(subjects: dict[str, str], artifact: dict[str, Any]) -> None:
    try:
        kind = artifact["kind"]
        reference = artifact["reference"].removeprefix("oci://")
        content_digest = artifact["content_digest"]
        evidence = artifact["evidence"]
    except (KeyError, TypeError, AttributeError) as exc:
        raise MirrorError(f"release artifact is incomplete at {exc}") from exc
    if kind not in {"container_image", "helm_chart", "oci_artifact"}:
        raise MirrorError(f"release artifact has unsupported kind {kind!r}")
    if DIGEST_RE.fullmatch(content_digest) is None:
        raise MirrorError(f"release artifact {reference!r} has an invalid content digest")
    expected_evidence = {
        "signature": f"cosign://{reference}",
        "sbom": f"cosign-attestation://{reference}#spdxjson",
        "provenance": f"cosign-attestation://{reference}#slsaprovenance",
    }
    if evidence != expected_evidence:
        raise MirrorError(f"release artifact {reference!r} has incomplete signed evidence")
    add_subject(subjects, reference, kind)


def release_subjects(manifest: dict[str, Any]) -> dict[str, str]:
    try:
        if manifest["schema_version"] != 1:
            raise MirrorError("unsupported release manifest schema")
        if VERSION_RE.fullmatch(manifest["release"]["version"]) is None:
            raise MirrorError("release manifest has an invalid release version")
        subjects: dict[str, str] = {}
        add_artifact(subjects, manifest["astronomer"]["chart"])
        for item in manifest["astronomer"]["images"]:
            add_artifact(subjects, item)
        for item in manifest["astronomer"]["runtime_images"]:
            add_subject(subjects, item["reference"], "container_image")
        add_artifact(subjects, manifest["flux"]["distribution"])
        for item in manifest["flux"]["controllers"]:
            add_artifact(subjects, item)
        add_artifact(subjects, manifest["built_in_bundles"]["artifact"])
        for component in manifest["built_in_bundles"]["components"]:
            for reference in component["images"]:
                add_subject(subjects, reference, "container_image")
        add_artifact(subjects, manifest["charlie"]["artifact"])
    except (KeyError, TypeError) as exc:
        raise MirrorError(f"release manifest is incomplete at {exc}") from exc
    return subjects


def plan(manifest_path: Path, destination_registry: str) -> dict[str, Any]:
    if REGISTRY_RE.fullmatch(destination_registry) is None:
        raise MirrorError("destination registry must be one lowercase host[:port] with no path or credentials")
    manifest = load_json(manifest_path)
    subjects = release_subjects(manifest)
    entries: list[dict[str, str]] = []
    rewrites: dict[str, str] = {}
    for source, kind in sorted(subjects.items()):
        match = REFERENCE_RE.fullmatch(source)
        assert match is not None
        target = f"{destination_registry}/{match.group('path')}@{match.group('digest')}"
        # Two registries may legitimately publish the same repository path and
        # digest. In that case both immutable sources collapse to one identical
        # destination blob; separate mapping entries retain both source keys.
        rewrites[match.group("registry")] = destination_registry
        entries.append(
            {
                "id": hashlib.sha256(source.encode("utf-8")).hexdigest()[:16],
                "kind": kind,
                "source": source,
                "target": target,
                "digest": match.group("digest"),
                "copy_tool": "skopeo" if kind == "container_image" else "oras",
            }
        )
    return {
        "schema_version": 1,
        "release_version": manifest["release"]["version"],
        "release_manifest_digest": file_digest(manifest_path),
        "destination_registry": destination_registry,
        "registry_rewrites": dict(sorted(rewrites.items())),
        "entries": entries,
    }


def run(command: list[str], *, capture: bool = False) -> str:
    try:
        completed = subprocess.run(
            command,
            check=True,
            text=True,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
            env=os.environ.copy(),
        )
    except FileNotFoundError as exc:
        raise MirrorError(f"required command is unavailable: {command[0]}") from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or "").strip()
        if len(detail) > 500:
            detail = detail[:500] + "..."
        raise MirrorError(f"{command[0]} failed for an immutable release subject: {detail or 'no diagnostic'}") from exc
    return (completed.stdout or "").strip()


def run_bytes(command: list[str]) -> bytes:
    try:
        completed = subprocess.run(
            command,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=os.environ.copy(),
        )
    except FileNotFoundError as exc:
        raise MirrorError(f"required command is unavailable: {command[0]}") from exc
    except subprocess.CalledProcessError as exc:
        detail = (exc.stderr or b"").decode("utf-8", errors="replace").strip()
        if len(detail) > 500:
            detail = detail[:500] + "..."
        raise MirrorError(f"{command[0]} failed for an immutable release subject: {detail or 'no diagnostic'}") from exc
    return completed.stdout


def resolved_digest(reference: str, tool: str) -> str:
    if tool == "skopeo":
        output = run_bytes(["skopeo", "inspect", "--raw", f"docker://{reference}"])
        return "sha256:" + hashlib.sha256(output).hexdigest()
    output = run(["oras", "manifest", "fetch", "--descriptor", reference], capture=True)
    try:
        value = json.loads(output)["digest"]
    except (json.JSONDecodeError, KeyError, TypeError) as exc:
        raise MirrorError(f"oras returned an invalid descriptor for {reference!r}") from exc
    if DIGEST_RE.fullmatch(value) is None:
        raise MirrorError(f"oras returned an invalid digest for {reference!r}")
    return value


def validate_plan(document: dict[str, Any]) -> list[dict[str, str]]:
    required_fields = {
        "schema_version", "release_version", "release_manifest_digest",
        "destination_registry", "registry_rewrites", "entries",
    }
    if set(document) != required_fields:
        raise MirrorError("mirror plan has unexpected or missing fields")
    if (
        document.get("schema_version") != 1
        or VERSION_RE.fullmatch(document.get("release_version", "")) is None
        or DIGEST_RE.fullmatch(document.get("release_manifest_digest", "")) is None
    ):
        raise MirrorError("invalid mirror plan metadata")
    destination = document.get("destination_registry", "")
    if REGISTRY_RE.fullmatch(destination) is None:
        raise MirrorError("mirror plan has an invalid destination registry")
    rewrites = document.get("registry_rewrites")
    if (
        not isinstance(rewrites, dict)
        or not rewrites
        or any(REGISTRY_RE.fullmatch(source) is None or target != destination for source, target in rewrites.items())
    ):
        raise MirrorError("mirror plan has invalid registry rewrites")
    entries = document.get("entries")
    if not isinstance(entries, list) or not entries:
        raise MirrorError("mirror plan has no entries")
    seen_ids: set[str] = set()
    seen_sources: set[str] = set()
    for entry in entries:
        if not isinstance(entry, dict) or set(entry) != {"id", "kind", "source", "target", "digest", "copy_tool"}:
            raise MirrorError("mirror plan entry has unexpected fields")
        if not all(isinstance(value, str) for value in entry.values()):
            raise MirrorError("mirror plan entry fields must be strings")
        expected_id = hashlib.sha256(entry.get("source", "").encode("utf-8")).hexdigest()[:16]
        if entry.get("id") != expected_id or entry["id"] in seen_ids:
            raise MirrorError("mirror plan entry has an invalid or duplicate ID")
        seen_ids.add(entry["id"])
        source_match = REFERENCE_RE.fullmatch(entry["source"])
        target_match = REFERENCE_RE.fullmatch(entry["target"])
        if (
            source_match is None
            or target_match is None
            or source_match.group("registry") not in rewrites
            or target_match.group("registry") != destination
            or source_match.group("digest") != entry["digest"]
            or target_match.group("digest") != entry["digest"]
            or entry["source"] in seen_sources
        ):
            raise MirrorError(f"mirror plan entry {entry['id']} is mutable or digest-inconsistent")
        seen_sources.add(entry["source"])
        if entry["kind"] not in {"container_image", "helm_chart", "oci_artifact"}:
            raise MirrorError(f"mirror plan entry {entry['id']} has an unsupported kind")
        expected_tool = "skopeo" if entry["kind"] == "container_image" else "oras"
        if entry["copy_tool"] != expected_tool:
            raise MirrorError(f"mirror plan entry {entry['id']} has the wrong copy tool")
    if entries != sorted(entries, key=lambda item: item["source"]):
        raise MirrorError("mirror plan entries are not deterministically ordered")
    return entries


def copy_reference(target: str) -> str:
    repository, digest = target.rsplit("@", 1)
    return f"{repository}:sha256-{digest.removeprefix('sha256:')}"


def install_values(manifest: dict[str, Any], document: dict[str, Any]) -> dict[str, Any]:
    """Generate exact Helm overrides from the signed release and mirror map."""
    entries = validate_plan(document)
    if document["release_manifest_digest"] != "sha256:" + hashlib.sha256(canonical(manifest)).hexdigest():
        # Release manifests are canonical in published kits. The caller checks
        # the exact on-disk digest separately; this catches programmatic misuse.
        raise MirrorError("mirror plan and canonical release manifest differ")
    targets = {entry["source"]: entry["target"] for entry in entries}

    values: dict[str, Any] = {
        "image": {"registry": ""},
        "delivery": {
            "artifacts": {
                "publicRegistry": "ghcr.io",
                "privateRegistry": document["destination_registry"],
                "registryRewrites": document["registry_rewrites"],
            }
        },
    }

    def set_nested(path: tuple[str, ...], value: Any) -> None:
        cursor = values
        for key in path[:-1]:
            cursor = cursor.setdefault(key, {})
        cursor[path[-1]] = value

    def target_parts(source: str) -> tuple[str, str, str]:
        try:
            target = targets[source.removeprefix("oci://")]
        except KeyError as exc:
            raise MirrorError(f"mirror plan has no install target for {source!r}") from exc
        match = REFERENCE_RE.fullmatch(target)
        assert match is not None
        return match.group("registry"), match.group("path"), match.group("digest")

    def set_image(path: tuple[str, ...], source: str) -> str:
        registry, repository, digest = target_parts(source)
        set_nested(path + ("registry",), registry)
        set_nested(path + ("repository",), repository)
        set_nested(path + ("digest",), digest)
        return f"{registry}/{repository}@{digest}"

    first_party_paths = {
        "server": ("image", "server"),
        "worker": ("image", "worker"),
        "agent": ("image", "agent"),
        "migrate": ("image", "migrate"),
        "frontend": ("frontend", "image"),
        "shell": ("preflight", "image"),
    }
    shell_target = ""
    for artifact in manifest["astronomer"]["images"]:
        path = first_party_paths.get(artifact["name"])
        if path is None:
            raise MirrorError(f"release has unknown first-party image {artifact['name']!r}")
        exact = set_image(path, artifact["reference"])
        if artifact["name"] == "shell":
            shell_target = exact
    if set(first_party_paths) != {item["name"] for item in manifest["astronomer"]["images"]}:
        raise MirrorError("release first-party image set is incomplete")
    set_nested(("kubectlShell", "image"), shell_target)

    runtime_paths: dict[str, tuple[tuple[str, ...], ...]] = {
        "busybox": (("utilities", "busybox"),),
        "postgres": (("postgres", "image"), ("managementRestoreDrill", "sidecar", "image")),
        "valkey/valkey": (("redis", "image"),),
        "dexidp/dex": (("dex", "image"),),
        "fluent/fluent-bit": (("managementLogging", "image"),),
        "ghcr.io/alphabravocompany/pgdump-s3": (
            ("managementBackup", "image"),
            ("managementRestoreDrill", "image"),
        ),
    }

    def source_repository(reference: str) -> str:
        value = reference.split("@", 1)[0]
        last = value.rsplit("/", 1)[-1]
        if ":" in last:
            value = value.rsplit(":", 1)[0]
        return value

    runtime_by_repository = {
        source_repository(item["source_reference"]): item["reference"]
        for item in manifest["astronomer"]["runtime_images"]
    }
    for repository, paths in runtime_paths.items():
        exact = runtime_by_repository.get(repository)
        if exact is None:
            raise MirrorError(f"release runtime inventory has no {repository!r} image")
        for path in paths:
            set_image(path, exact)

    signing = manifest["release"]["artifact_signing_policy"]
    for manifest_key, values_key in (
        (("flux", "distribution"), "fluxDistribution"),
        (("built_in_bundles", "artifact"), "builtInBundles"),
    ):
        artifact = manifest
        for key in manifest_key:
            artifact = artifact[key]
        registry, repository, digest = target_parts(artifact["reference"])
        values["delivery"]["artifacts"][values_key] = {
            "ociRepository": f"{registry}/{repository}",
            "digest": digest,
            "disconnectedAssetPath": "",
            "trustPolicy": {
                "requireSignature": True,
                "certificateIdentity": signing["certificate_identity"],
                "oidcIssuer": signing["certificate_oidc_issuer"],
            },
        }
    return values


def apply(document: dict[str, Any]) -> None:
    entries = validate_plan(document)
    run([str(ROOT / "scripts/check-build-capacity.sh"), "--path", str(ROOT)])
    for entry in entries:
        existing: str | None = None
        try:
            existing = resolved_digest(entry["target"], entry["copy_tool"])
        except MirrorError:
            existing = None
        if existing is not None and existing != entry["digest"]:
            raise MirrorError(f"destination for {entry['id']} resolves to {existing}, expected {entry['digest']}")
        if existing == entry["digest"]:
            print(f"mirror-release: verified existing {entry['id']} {entry['target']}")
            continue
        destination = copy_reference(entry["target"])
        if entry["copy_tool"] == "skopeo":
            run(
                [
                    "skopeo", "copy", "--all", "--preserve-digests",
                    f"docker://{entry['source']}", f"docker://{destination}",
                ]
            )
        else:
            # Recursive copy preserves the OCI referrer graph containing
            # signatures, SPDX attestations, and SLSA provenance.
            run(["oras", "copy", "--recursive", entry["source"], destination])
        actual = resolved_digest(entry["target"], entry["copy_tool"])
        if actual != entry["digest"]:
            raise MirrorError(f"copied destination for {entry['id']} resolves to {actual}, expected {entry['digest']}")
        print(f"mirror-release: copied {entry['id']} {entry['target']}")


def verify(document: dict[str, Any]) -> None:
    for entry in validate_plan(document):
        actual = resolved_digest(entry["target"], entry["copy_tool"])
        if actual != entry["digest"]:
            raise MirrorError(f"mirror verification failed for {entry['id']}: {actual} != {entry['digest']}")
        print(f"mirror-release: verified {entry['id']} {entry['target']}")


def sign(plan_path: Path, signature_output: Path, key: str | None) -> None:
    signature_output.parent.mkdir(parents=True, exist_ok=True)
    command = ["cosign", "sign-blob", "--yes", "--bundle", str(signature_output)]
    if key:
        command.extend(["--key", key])
    command.append(str(plan_path))
    run(command)


def write_atomic(path: Path, payload: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.tmp.{os.getpid()}")
    try:
        with temporary.open("xb") as handle:
            os.chmod(temporary, 0o600)
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except OSError as exc:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass
        raise MirrorError(f"cannot write {path}: {exc}") from exc


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    subparsers = result.add_subparsers(dest="command", required=True)
    plan_parser = subparsers.add_parser("plan", help="write a deterministic copy/rewrite plan without registry writes")
    plan_parser.add_argument("--manifest", required=True, type=Path)
    plan_parser.add_argument("--destination-registry", required=True)
    plan_parser.add_argument("--output", required=True, type=Path)
    plan_parser.add_argument("--values-output", type=Path, help="write exact Helm JSON overrides for the mirrored release")
    for name in ("apply", "verify"):
        child = subparsers.add_parser(name)
        child.add_argument("--plan", required=True, type=Path)
        if name == "apply":
            child.add_argument("--signature-output", required=True, type=Path)
            child.add_argument("--cosign-key")
    return result


def main() -> int:
    args = parser().parse_args()
    try:
        if args.command == "plan":
            manifest = load_json(args.manifest)
            document = plan(args.manifest, args.destination_registry)
            payload = canonical(document)
            write_atomic(args.output, payload)
            if args.values_output is not None:
                write_atomic(args.values_output, canonical(install_values(manifest, document)))
            for entry in document["entries"]:
                print(f"mirror-release: plan {entry['id']} {entry['source']} -> {entry['target']}")
        else:
            document = load_json(args.plan)
            if canonical(document) != args.plan.read_bytes():
                raise MirrorError("mirror plan is not canonical; regenerate it before use")
            if args.command == "apply":
                apply(document)
                verify(document)
                sign(args.plan, args.signature_output, args.cosign_key)
            else:
                verify(document)
        return 0
    except MirrorError as exc:
        print(f"mirror-release: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
