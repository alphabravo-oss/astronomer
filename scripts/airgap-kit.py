#!/usr/bin/env python3
"""Build and apply the OSS air-gap kit for one immutable Astronomer release.

The kit is a small tarball: signed release manifest, Helm chart, delivery
archives, a complete digest-pinned container-image list, and save/load
helpers. It does not contain container image blobs, registry credentials,
or Helm secret values. Operators run save on a connected host and load on
the dark-site registry — the same split Rancher uses.

Image identity and registry rewrite come from scripts/mirror-release.py so
the kit cannot drift from the signed mapping used by Helm.
"""

from __future__ import annotations

import argparse
import gzip
import hashlib
import importlib.util
import io
import json
import os
import re
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path
from pathlib import PurePosixPath
from typing import Any


SCRIPT_DIR = Path(__file__).resolve().parent
DIGEST_RE = re.compile(r"^sha256:[a-f0-9]{64}$")
REGISTRY_RE = re.compile(r"^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$")
FORBIDDEN_ARG = re.compile(r"(password|passwd|secret|token|credential)", re.IGNORECASE)
COSIGN_ISSUER = "https://token.actions.githubusercontent.com"
COSIGN_WORKFLOW = "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/{version}"
MAX_INDEX_BYTES = 4 * 1024 * 1024
MAX_ARCHIVE_MEMBERS = 1_000_000

KIT_README = """# Astronomer air-gap kit

This directory is one immutable `{version}` release for a disconnected install.
It does **not** include container image blobs (those exceed GitHub's asset
limit). Create them on a connected host, then load them in the dark site.

Do not put registry passwords, TLS keys, or Helm secrets in this directory.

## 1. Verify

```bash
sha256sum --check SHA256SUMS
cosign verify-blob \\
  --bundle release-manifest.sigstore.json \\
  --certificate-identity \\
    "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/{version}" \\
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \\
  release-manifest.json
```

`astronomer-images.txt` is the complete digest-pinned container list derived
from that manifest (management images, runtime images, Flux controllers,
built-in bundle images, Charlie).

## 2. Connected host — save images

Requires Skopeo. Default is `linux/amd64` only.

```bash
./astronomer-save-images.sh \\
  --manifest release-manifest.json \\
  --output astronomer-images.tar.gz
```

Pass `--all-platforms` for the full multi-arch index. Pass `--first-party`
to copy only the seven Astronomer images (smoke / smaller USB).

## 3. Dark site — load images

Authenticate Skopeo to the private registry using its normal credential file.
This script does not accept passwords.

```bash
./astronomer-load-images.sh \\
  --manifest release-manifest.json \\
  --signature release-manifest.sigstore.json \\
  --images astronomer-images.tar.gz \\
  --destination-registry registry.internal.example.com \\
  --values-output airgap-values.json
```

## 4. Install

Use the included chart, `values-production.yaml`, `airgap-values.json`, and
`--set-file release.manifest=release-manifest.json`. Flux and built-in bundle
archives in this kit are the disconnected assets for those artifacts.

Registry-to-registry copy without a USB stick remains
`scripts/mirror-release.py` from a full Astronomer checkout.
"""


class KitError(ValueError):
    """A user-actionable air-gap kit contract violation."""


def refuse_secrets(argv: list[str]) -> None:
    for value in argv:
        if FORBIDDEN_ARG.search(value):
            raise KitError("air-gap kit commands do not accept credentials, tokens, or secret values")


def load_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise KitError(f"cannot read JSON {path}: {exc}") from exc


def load_mirror():
    path = SCRIPT_DIR / "mirror-release.py"
    spec = importlib.util.spec_from_file_location("astronomer_mirror_release", path)
    if spec is None or spec.loader is None:
        raise KitError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def canonical(value: Any) -> bytes:
    return (json.dumps(value, sort_keys=True, indent=2, ensure_ascii=False) + "\n").encode("utf-8")


def sha256_path(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def container_images(manifest: dict[str, Any], *, first_party: bool = False) -> list[str]:
    mirror = load_mirror()
    subjects = mirror.release_subjects(manifest)
    images = sorted(reference for reference, kind in subjects.items() if kind == "container_image")
    if not first_party:
        return images
    wanted = {item["reference"].removeprefix("oci://") for item in manifest["astronomer"]["images"]}
    filtered = [reference for reference in images if reference in wanted]
    if len(filtered) != 7:
        raise KitError("release manifest must list exactly seven first-party container images")
    return filtered


def images_txt(manifest: dict[str, Any], *, first_party: bool = False) -> str:
    version = manifest["release"]["version"]
    lines = [
        f"# Astronomer {version} container images (digest-pinned).",
        "# Derived from release-manifest.json. Do not edit.",
        "# One repository@sha256 line. No tags, credentials, or Helm values.",
    ]
    images = container_images(manifest, first_party=first_party)
    if not images:
        raise KitError("release manifest lists no container images")
    for reference in images:
        if "@sha256:" not in reference:
            raise KitError(f"container image is not digest-pinned: {reference!r}")
        lines.append(reference)
    return "\n".join(lines) + "\n"


def destination_for(source: str, destination_registry: str) -> str:
    if REGISTRY_RE.fullmatch(destination_registry) is None:
        raise KitError("destination registry must be one lowercase host[:port] with no path or credentials")
    mirror = load_mirror()
    match = mirror.REFERENCE_RE.fullmatch(source.removeprefix("oci://"))
    if match is None:
        raise KitError(f"container image is not an immutable repository@sha256 reference: {source!r}")
    return f"{destination_registry}/{match.group('path')}@{match.group('digest')}"


def copy_destination(target: str) -> str:
    repository, digest = target.rsplit("@", 1)
    return f"{repository}:sha256-{digest.removeprefix('sha256:')}"


def digest_dir(digest: str) -> str:
    if DIGEST_RE.fullmatch(digest) is None:
        raise KitError(f"invalid image digest {digest!r}")
    return digest.removeprefix("sha256:")


def run(command: list[str], failure: str = "copying an immutable image") -> None:
    try:
        subprocess.run(command, check=True, env=os.environ.copy())
    except FileNotFoundError as exc:
        raise KitError(f"required command is unavailable: {command[0]}") from exc
    except subprocess.CalledProcessError as exc:
        raise KitError(f"{command[0]} failed {failure}") from exc


def verify_release_manifest(manifest_path: Path, signature: Path, manifest: dict[str, Any]) -> None:
    version = manifest.get("release", {}).get("version")
    if not isinstance(version, str) or re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?", version) is None:
        raise KitError("release manifest has an invalid release.version")
    if not signature.is_file():
        raise KitError("release manifest Sigstore bundle is required before loading images")
    run(
        [
            "cosign",
            "verify-blob",
            "--bundle",
            str(signature),
            "--certificate-identity",
            COSIGN_WORKFLOW.format(version=version),
            "--certificate-oidc-issuer",
            COSIGN_ISSUER,
            str(manifest_path),
        ],
        "verifying the release manifest Sigstore identity",
    )


def write_kit_file(root: Path, relative: str, data: bytes, mode: int = 0o644) -> None:
    path = root / relative
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    path.chmod(mode)


def copy_kit_file(root: Path, relative: str, source: Path, mode: int | None = None) -> None:
    if not source.is_file():
        raise KitError(f"air-gap kit is missing required file {source}")
    data = source.read_bytes()
    write_kit_file(root, relative, data, mode if mode is not None else (source.stat().st_mode & 0o777))


def pack(
    *,
    manifest_path: Path,
    chart_package: Path,
    values_production: Path,
    output: Path,
    signature: Path | None = None,
    flux_archive: Path | None = None,
    bundles_archive: Path | None = None,
) -> Path:
    manifest = load_json(manifest_path)
    version = manifest["release"]["version"]
    if not version.startswith("v"):
        raise KitError("release version must be vX.Y.Z")
    kit_name = f"astronomer-airgap-{version}"
    listing = images_txt(manifest)
    with tempfile.TemporaryDirectory() as temp:
        root = Path(temp) / kit_name
        root.mkdir()
        copy_kit_file(root, "release-manifest.json", manifest_path)
        if signature is not None:
            copy_kit_file(root, "release-manifest.sigstore.json", signature)
        copy_kit_file(root, chart_package.name, chart_package)
        copy_kit_file(root, "values-production.yaml", values_production)
        if flux_archive is not None:
            copy_kit_file(root, "flux-distribution.tar.gz", flux_archive)
        if bundles_archive is not None:
            copy_kit_file(root, "builtin-bundles.tar.gz", bundles_archive)
        write_kit_file(root, "astronomer-images.txt", listing.encode("utf-8"))
        write_kit_file(root, "README.md", KIT_README.format(version=version).encode("utf-8"))
        for name, mode in (
            ("airgap-kit.py", 0o755),
            ("mirror-release.py", 0o755),
            ("astronomer-save-images.sh", 0o755),
            ("astronomer-load-images.sh", 0o755),
        ):
            copy_kit_file(root, name, SCRIPT_DIR / name, mode)
        members = sorted(path for path in root.rglob("*") if path.is_file())
        checksums = []
        for path in members:
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            checksums.append(f"{digest}  {path.relative_to(root).as_posix()}\n")
        write_kit_file(root, "SHA256SUMS", "".join(checksums).encode("utf-8"))
        members = sorted(path for path in root.rglob("*") if path.is_file())
        output.parent.mkdir(parents=True, exist_ok=True)
        raw = io.BytesIO()
        with tarfile.open(fileobj=raw, mode="w") as archive:
            for path in members:
                info = archive.gettarinfo(path, arcname=f"{kit_name}/{path.relative_to(root).as_posix()}")
                info.mtime = 0
                info.uid = 0
                info.gid = 0
                info.uname = ""
                info.gname = ""
                with path.open("rb") as handle:
                    archive.addfile(info, handle)
        raw.seek(0)
        with output.open("wb") as handle:
            with gzip.GzipFile(filename="", mode="wb", fileobj=handle, mtime=0) as compressed:
                compressed.write(raw.getvalue())
        output.chmod(0o644)
    return output


def save(
    *,
    manifest_path: Path,
    output: Path,
    os_name: str = "linux",
    arch: str = "amd64",
    all_platforms: bool = False,
    first_party: bool = False,
) -> None:
    if all_platforms and (os_name != "linux" or arch != "amd64"):
        raise KitError("--all-platforms cannot be combined with a platform override")
    manifest = load_json(manifest_path)
    images = container_images(manifest, first_party=first_party)
    with tempfile.TemporaryDirectory() as temp:
        root = Path(temp)
        records = []
        for source in images:
            digest = source.rsplit("@", 1)[1]
            directory = root / digest_dir(digest)
            directory.mkdir(exist_ok=True)
            if not any(directory.iterdir()):
                command = ["skopeo", "copy", "--preserve-digests"]
                if all_platforms:
                    command.append("--all")
                else:
                    command.extend(["--override-os", os_name, "--override-arch", arch])
                command.extend([f"docker://{source}", f"dir:{directory}"])
                run(command)
            records.append({"source": source, "digest": digest, "dir": digest_dir(digest)})
        payloads: dict[str, str] = {}
        for record in records:
            directory = root / record["dir"]
            for path in sorted(item for item in directory.rglob("*") if item.is_file()):
                relative = path.relative_to(root).as_posix()
                payloads[relative] = sha256_path(path)
        index = {
            "schema_version": 1,
            "release_version": manifest["release"]["version"],
            "release_manifest_sha256": sha256_path(manifest_path),
            "scope": "first_party" if first_party else "all",
            "os": None if all_platforms else os_name,
            "arch": None if all_platforms else arch,
            "all_platforms": all_platforms,
            "images": records,
            "members": payloads,
        }
        (root / "index.json").write_bytes(canonical(index))
        output.parent.mkdir(parents=True, exist_ok=True)
        with tarfile.open(output, mode="w:gz") as archive:
            archive.add(root / "index.json", arcname="index.json")
            seen_dirs: set[str] = set()
            for record in records:
                if record["dir"] in seen_dirs:
                    continue
                seen_dirs.add(record["dir"])
                archive.add(root / record["dir"], arcname=record["dir"])


def validated_member_name(name: str) -> PurePosixPath:
    if not name or "\\" in name or "\x00" in name:
        raise KitError(f"image archive contains an unsafe member path: {name!r}")
    path = PurePosixPath(name)
    if path.is_absolute() or any(part in ("", ".", "..") for part in path.parts) or path.as_posix() != name:
        raise KitError(f"image archive contains an unsafe member path: {name!r}")
    return path


def validate_image_archive(
    archive: tarfile.TarFile, manifest: dict[str, Any], manifest_path: Path
) -> tuple[dict[str, Any], list[tarfile.TarInfo]]:
    members = archive.getmembers()
    if not members or len(members) > MAX_ARCHIVE_MEMBERS:
        raise KitError("image archive has an invalid member count")
    names: set[str] = set()
    index_member: tarfile.TarInfo | None = None
    for member in members:
        validated_member_name(member.name)
        if member.name in names:
            raise KitError(f"image archive contains duplicate member {member.name!r}")
        names.add(member.name)
        if not (member.isfile() or member.isdir()):
            raise KitError(f"image archive member {member.name!r} is not a regular file or directory")
        if member.name == "index.json":
            if not member.isfile() or member.size > MAX_INDEX_BYTES:
                raise KitError("image archive index.json is not a bounded regular file")
            index_member = member
    if index_member is None:
        raise KitError("image archive is missing index.json")
    handle = archive.extractfile(index_member)
    if handle is None:
        raise KitError("image archive index.json is unreadable")
    try:
        index = json.loads(handle.read().decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise KitError(f"image archive index.json is invalid: {exc}") from exc
    expected_index_keys = {
        "schema_version",
        "release_version",
        "release_manifest_sha256",
        "scope",
        "os",
        "arch",
        "all_platforms",
        "images",
        "members",
    }
    if not isinstance(index, dict) or set(index) != expected_index_keys or index.get("schema_version") != 1:
        raise KitError("image archive index is not the closed v1 Astronomer schema")
    if index["release_version"] != manifest.get("release", {}).get("version"):
        raise KitError("image archive belongs to a different release")
    if index["release_manifest_sha256"] != sha256_path(manifest_path):
        raise KitError("image archive is not bound to this release manifest")
    scope = index["scope"]
    if scope not in ("all", "first_party"):
        raise KitError("image archive has an invalid image scope")
    image_records = index["images"]
    if not isinstance(image_records, list) or not image_records:
        raise KitError("image archive index lists no images")
    expected_images = set(container_images(manifest, first_party=scope == "first_party"))
    saved_images: set[str] = set()
    allowed_dirs: set[str] = set()
    for item in image_records:
        if not isinstance(item, dict) or set(item) != {"source", "digest", "dir"}:
            raise KitError("image archive contains an invalid image record")
        source, digest, directory = item["source"], item["digest"], item["dir"]
        if not all(isinstance(value, str) for value in (source, digest, directory)):
            raise KitError("image archive image record fields must be strings")
        if source in saved_images or digest_dir(digest) != directory or source.rsplit("@", 1)[-1] != digest:
            raise KitError("image archive contains duplicate or inconsistent image identity")
        saved_images.add(source)
        allowed_dirs.add(directory)
    if saved_images != expected_images:
        raise KitError("image archive image set does not exactly match its signed release scope")

    checksums = index["members"]
    if not isinstance(checksums, dict) or not checksums:
        raise KitError("image archive has no authenticated payload inventory")
    regular_names = {member.name for member in members if member.isfile() and member.name != "index.json"}
    if set(checksums) != regular_names:
        raise KitError("image archive member set differs from the authenticated index")
    for name, expected_digest in checksums.items():
        path = validated_member_name(name)
        if path.parts[0] not in allowed_dirs or not isinstance(expected_digest, str) or re.fullmatch(r"[a-f0-9]{64}", expected_digest) is None:
            raise KitError(f"image archive index contains invalid payload {name!r}")
    for member in members:
        if member.name == "index.json":
            continue
        path = validated_member_name(member.name)
        if path.parts[0] not in allowed_dirs:
            raise KitError(f"image archive contains extra payload {member.name!r}")
        if member.isfile():
            payload = archive.extractfile(member)
            if payload is None:
                raise KitError(f"image archive member {member.name!r} is unreadable")
            digest = hashlib.sha256()
            for chunk in iter(lambda: payload.read(1024 * 1024), b""):
                digest.update(chunk)
            if digest.hexdigest() != checksums[member.name]:
                raise KitError(f"image archive member checksum mismatch: {member.name}")
    return index, members


def extract_validated_archive(archive: tarfile.TarFile, root: Path, members: list[tarfile.TarInfo]) -> None:
    for member in members:
        if member.name == "index.json":
            continue
        relative = validated_member_name(member.name)
        target = root.joinpath(*relative.parts)
        if member.isdir():
            target.mkdir(parents=True, exist_ok=True)
            continue
        target.parent.mkdir(parents=True, exist_ok=True)
        source = archive.extractfile(member)
        if source is None:
            raise KitError(f"image archive member {member.name!r} is unreadable")
        try:
            with target.open("xb") as destination:
                while chunk := source.read(1024 * 1024):
                    destination.write(chunk)
        except FileExistsError as exc:
            raise KitError(f"image archive extraction would overwrite {member.name!r}") from exc


def load(
    *,
    manifest_path: Path,
    signature: Path,
    images_archive: Path,
    destination_registry: str,
    values_output: Path | None = None,
) -> None:
    manifest = load_json(manifest_path)
    verify_release_manifest(manifest_path, signature, manifest)
    mirror = load_mirror()
    with tempfile.TemporaryDirectory() as temp:
        root = Path(temp)
        with tarfile.open(images_archive, mode="r:gz") as archive:
            index, members = validate_image_archive(archive, manifest, manifest_path)
            extract_validated_archive(archive, root, members)
        for item in index["images"]:
            source = item["source"]
            digest = item["digest"]
            directory = root / item["dir"]
            if not directory.is_dir():
                raise KitError(f"image archive is missing {item['dir']}")
            target = destination_for(source, destination_registry)
            if not target.endswith("@" + digest):
                raise KitError(f"destination rewrite changed digest for {source}")
            run(
                [
                    "skopeo",
                    "copy",
                    "--preserve-digests",
                    f"dir:{directory}",
                    f"docker://{copy_destination(target)}",
                ]
            )
    if values_output is not None:
        mapping = mirror.plan(manifest_path, destination_registry)
        values = mirror.install_values(manifest, mapping)
        values_output.write_bytes(canonical(values))


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    sub = result.add_subparsers(dest="command", required=True)

    listing = sub.add_parser("list-images", help="Print digest-pinned container images from a release manifest")
    listing.add_argument("--manifest", type=Path, required=True)
    listing.add_argument("--first-party", action="store_true")

    packing = sub.add_parser("pack", help="Pack the small OSS air-gap kit (no image blobs)")
    packing.add_argument("--manifest", type=Path, required=True)
    packing.add_argument("--chart", type=Path, required=True)
    packing.add_argument("--values", type=Path, required=True)
    packing.add_argument("--output", type=Path, required=True)
    packing.add_argument("--signature", type=Path)
    packing.add_argument("--flux-archive", type=Path)
    packing.add_argument("--bundles-archive", type=Path)

    saving = sub.add_parser("save", help="Copy release container images into a local archive (connected host)")
    saving.add_argument("--manifest", type=Path, required=True)
    saving.add_argument("--output", type=Path, required=True)
    saving.add_argument("--os", dest="os_name", default="linux")
    saving.add_argument("--arch", default="amd64")
    saving.add_argument("--all-platforms", action="store_true")
    saving.add_argument("--first-party", action="store_true")

    loading = sub.add_parser("load", help="Copy a saved archive into a private registry (dark site)")
    loading.add_argument("--manifest", type=Path, required=True)
    loading.add_argument("--signature", type=Path, required=True)
    loading.add_argument("--images", type=Path, required=True)
    loading.add_argument("--destination-registry", required=True)
    loading.add_argument("--values-output", type=Path)
    return result


def main(argv: list[str] | None = None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    try:
        refuse_secrets(argv)
        args = parser().parse_args(argv)
        if args.command == "list-images":
            sys.stdout.write(images_txt(load_json(args.manifest), first_party=args.first_party))
        elif args.command == "pack":
            pack(
                manifest_path=args.manifest,
                chart_package=args.chart,
                values_production=args.values,
                output=args.output,
                signature=args.signature,
                flux_archive=args.flux_archive,
                bundles_archive=args.bundles_archive,
            )
        elif args.command == "save":
            save(
                manifest_path=args.manifest,
                output=args.output,
                os_name=args.os_name,
                arch=args.arch,
                all_platforms=args.all_platforms,
                first_party=args.first_party,
            )
        elif args.command == "load":
            load(
                manifest_path=args.manifest,
                signature=args.signature,
                images_archive=args.images,
                destination_registry=args.destination_registry,
                values_output=args.values_output,
            )
        return 0
    except KitError as exc:
        print(f"airgap-kit: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
