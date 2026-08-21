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
from typing import Any


SCRIPT_DIR = Path(__file__).resolve().parent
DIGEST_RE = re.compile(r"^sha256:[a-f0-9]{64}$")
REGISTRY_RE = re.compile(r"^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$")
FORBIDDEN_ARG = re.compile(r"(password|passwd|secret|token|credential)", re.IGNORECASE)

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
to copy only the six Astronomer images (smoke / smaller USB).

## 3. Dark site — load images

Authenticate Skopeo to the private registry using its normal credential file.
This script does not accept passwords.

```bash
./astronomer-load-images.sh \\
  --manifest release-manifest.json \\
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


def container_images(manifest: dict[str, Any], *, first_party: bool = False) -> list[str]:
    mirror = load_mirror()
    subjects = mirror.release_subjects(manifest)
    images = sorted(reference for reference, kind in subjects.items() if kind == "container_image")
    if not first_party:
        return images
    wanted = {item["reference"].removeprefix("oci://") for item in manifest["astronomer"]["images"]}
    filtered = [reference for reference in images if reference in wanted]
    if len(filtered) != 6:
        raise KitError("release manifest must list exactly six first-party container images")
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


def run(command: list[str]) -> None:
    try:
        subprocess.run(command, check=True, env=os.environ.copy())
    except FileNotFoundError as exc:
        raise KitError(f"required command is unavailable: {command[0]}") from exc
    except subprocess.CalledProcessError as exc:
        raise KitError(f"{command[0]} failed copying an immutable image") from exc


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
        index = {
            "schema_version": 1,
            "release_version": manifest["release"]["version"],
            "os": None if all_platforms else os_name,
            "arch": None if all_platforms else arch,
            "all_platforms": all_platforms,
            "images": records,
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


def load(
    *,
    manifest_path: Path,
    images_archive: Path,
    destination_registry: str,
    values_output: Path | None = None,
) -> None:
    manifest = load_json(manifest_path)
    mirror = load_mirror()
    with tempfile.TemporaryDirectory() as temp:
        root = Path(temp)
        with tarfile.open(images_archive, mode="r:gz") as archive:
            try:
                archive.extractall(root, filter="data")
            except TypeError:
                archive.extractall(root)
        index_path = root / "index.json"
        if not index_path.is_file():
            raise KitError("image archive is missing index.json")
        index = load_json(index_path)
        if index.get("schema_version") != 1 or not index.get("images"):
            raise KitError("image archive index is not a v1 Astronomer save")
        saved = {item["source"] for item in index["images"]}
        allowed = set(container_images(manifest))
        if not saved or not saved.issubset(allowed):
            raise KitError("image archive contains a container image that is not in the release manifest")
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
