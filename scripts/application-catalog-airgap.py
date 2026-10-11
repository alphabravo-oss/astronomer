#!/usr/bin/env python3
"""Prepare and import an authenticated closed application chart/profile archive.

Preparation never fetches mutable chart versions. Import preserves original
publisher bytes, and does not promote this archive into a qualified OCI catalog.
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
from pathlib import Path
import shutil
import stat
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parent.parent
MAX_ARCHIVE = 128 << 20
MAX_EXPANDED = 256 << 20
MAX_MEMBERS = 4096


class CatalogArchiveError(ValueError):
    pass


def sha(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def encoded(value: object) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def bounded_regular(path: Path, maximum: int) -> bytes:
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode) or not 0 < metadata.st_size <= maximum:
        raise CatalogArchiveError("bounded regular input required")
    with path.open("rb") as source:
        opened = os.fstat(source.fileno())
        if not stat.S_ISREG(opened.st_mode) or not 0 < opened.st_size <= maximum:
            raise CatalogArchiveError("bounded regular input required")
        data = source.read(maximum + 1)
    if len(data) > maximum:
        raise CatalogArchiveError("input exceeds bounds")
    return data


def run(command: list[str]) -> bytes:
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise CatalogArchiveError("canonical preparation/authentication command rejected input")
    return result.stdout


def verifier_command(args: argparse.Namespace) -> list[str]:
    # Import has no go-run, binary download, CLI offline flag or trust fetch fallback.
    binary = args.verifier_bin.resolve()
    if not binary.is_file() or not os.access(binary, os.X_OK):
        raise CatalogArchiveError("qualified local distribution verifier binary required")
    return [str(binary)]


def export(args: argparse.Namespace) -> None:
    if re.fullmatch(r"v[1-9][0-9]*\.[0-9]+\.[0-9]+", args.release) is None:
        raise CatalogArchiveError("exact supported release identity required")
    report = json.loads(run(verifier_command(args) + ["--application-profiles", str(args.profile_index),
                         "--catalog", str(args.catalog), "--platform-evidence", str(args.platform_evidence)]))
    catalog = bounded_regular(args.catalog, 4 << 20)
    if sha(catalog) != report["catalog_digest"]:
        raise CatalogArchiveError("catalog changed after profile render")
    members = {"catalog.json": catalog}
    categories = {"catalog.json": "catalog"}
    profiles = []
    for item in report["profiles"]:
        source, rendered = item["input"], item["rendered"]
        name = source["application"] + "-" + source["profile"]
        archive = bounded_regular(Path(source["archive"]), MAX_ARCHIVE)
        if sha(archive) != source["archive_digest"]:
            raise CatalogArchiveError("chart changed after profile render")
        values_path, inventory_path = f"profiles/{name}.values.json", f"profiles/{name}.images.json"
        additions = {f"charts/{name}.tgz": (archive, "chart"), values_path: (encoded(rendered["values"]), "values"),
                     inventory_path: (encoded({"schema_version": 1, "images": [
                         {"reference": image, "platforms": ["linux/amd64", "linux/arm64"], "workloads": [source["application"]]}
                         for image in rendered["images"]]}), "image_inventory"),
                     f"profiles/{name}.render.json": (encoded(item), "values")}
        for path, (data, category) in additions.items():
            if path in members:
                raise CatalogArchiveError("application profile member identity collides")
            members[path], categories[path] = data, category
        profiles.append({"name": name, "qualification": "qualified", "values_path": values_path,
                         "inventory_path": inventory_path, "required_categories": ["catalog", "chart", "values", "image_inventory"],
                         "blocked_categories": [], "capabilities": []})
    bom = {"schema_version": 1, "asset_contract_version": 1, "assets": [], "release": args.release, "catalog_digest": sha(catalog), "charts": [], "profiles": profiles,
           "members": [{"path": name, "digest": sha(data), "size": len(data), "category": categories[name]}
                       for name, data in sorted(members.items())]}
    members["bom.json"] = encoded(bom)
    if len(members) > MAX_MEMBERS or sum(map(len, members.values())) > MAX_EXPANDED:
        raise CatalogArchiveError("application archive scope exceeds bounds")
    raw = io.BytesIO()
    with gzip.GzipFile(fileobj=raw, mode="wb", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
            for name, data in sorted(members.items()):
                member = tarfile.TarInfo(name)
                member.size, member.mode = len(data), 0o600
                archive.addfile(member, io.BytesIO(data))
    payload = raw.getvalue()
    if len(payload) > MAX_ARCHIVE:
        raise CatalogArchiveError("compressed archive exceeds bounds")
    transfer = encoded({"schema_version": 1, "kind": "application_catalog_transfer", "release": args.release,
                        "catalog_digest": sha(catalog), "archive_digest": sha(payload), "bom_digest": sha(members["bom.json"]),
                        "publisher_mode": args.publisher_mode})
    # Refuse overwrite: the archive, external document and signature are one unit.
    for path in (args.output, args.bom, args.signature):
        if path.exists():
            raise CatalogArchiveError("new output paths required")
        path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="astronomer-catalog-prepare-") as temporary:
        directory = Path(temporary)
        document = directory / "transfer.json"
        document.write_bytes(transfer)
        signature = directory / "transfer.sigstore.json"
        command = [str(args.cosign_bin), "sign-blob", "--yes", "--use-signing-config=false", "--bundle", str(signature)]
        if args.publisher_mode == "qa":
            if args.signing_key is None:
                raise CatalogArchiveError("QA signing key required")
            command += ["--key", str(args.signing_key), "--tlog-upload=false"]
        elif args.signing_key is not None:
            raise CatalogArchiveError("production publisher requires its authorized OIDC identity")
        run(command + [str(document)])
        signed = bounded_regular(signature, 1 << 20)
        for path, data in ((args.output, payload), (args.bom, transfer), (args.signature, signed)):
            with path.open("xb") as destination:
                destination.write(data)
            path.chmod(0o600)


def import_archive(args: argparse.Namespace) -> None:
    if args.destination.exists():
        raise CatalogArchiveError("new extraction destination required")
    # Own an immutable snapshot before verification; copying bytes is not extraction.
    with tempfile.TemporaryDirectory(prefix="astronomer-catalog-import-") as temporary:
        root = Path(temporary)
        archive_path, document, signature = root / "archive.tgz", root / "transfer.json", root / "signature.json"
        archive_path.write_bytes(bounded_regular(args.archive, MAX_ARCHIVE))
        document.write_bytes(bounded_regular(args.bom, 1 << 20))
        signature.write_bytes(bounded_regular(args.signature, 1 << 20))
        command = verifier_command(args) + ["--verify-application-archive", str(archive_path), "--verify-document", str(document),
                  "--signature", str(signature), "--trust-directory", str(args.trust_directory.resolve()),
                  "--signature-provider", args.signature_provider]
        for option, value in (("--key-ref", args.key_ref), ("--certificate-identity", args.certificate_identity),
                              ("--certificate-oidc-issuer", args.certificate_oidc_issuer)):
            if value:
                command += [option, value]
        run(command)  # canonical signature, archive, values/render and inventory checks BEFORE extraction
        spec = importlib.util.spec_from_file_location("airgap_kit", ROOT / "scripts/airgap-kit.py")
        assert spec and spec.loader
        kit = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(kit)
        extracted = root / "extracted"
        extracted.mkdir()
        with tarfile.open(archive_path, "r:gz") as archive:
            members = archive.getmembers()
            kit.extract_validated_archive(archive, extracted, members)
        # Preserve original catalog, BOM, signing evidence and source URLs exactly.
        (extracted / "transfer.json").write_bytes(document.read_bytes())
        (extracted / "transfer.sigstore.json").write_bytes(signature.read_bytes())
        args.destination.parent.mkdir(parents=True, exist_ok=True)
        if args.destination.exists():
            raise CatalogArchiveError("extraction destination appeared during verification")
        shutil.copytree(extracted, args.destination, dirs_exist_ok=False)
    print(json.dumps({"verification": "verified", "inventory": "complete_for_reviewed_profiles",
                      "runtime_qualification": "unavailable_until_signed_local_OCI_catalog_and_child_subjects_qualified"}))


def parser() -> argparse.ArgumentParser:
    cli = argparse.ArgumentParser(description=__doc__)
    operations = cli.add_subparsers(dest="operation", required=True)
    prepare = operations.add_parser("export")
    for name in ("catalog", "profile-index", "platform-evidence", "output", "bom", "signature", "verifier-bin", "cosign-bin"):
        prepare.add_argument("--" + name, type=Path, required=True)
    prepare.add_argument("--release", required=True)
    prepare.add_argument("--publisher-mode", choices=("production", "qa"), default="production")
    prepare.add_argument("--signing-key", type=Path)
    consume = operations.add_parser("import")
    for name in ("archive", "bom", "signature", "destination", "verifier-bin", "trust-directory"):
        consume.add_argument("--" + name, type=Path, required=True)
    consume.add_argument("--signature-provider", choices=("cosign_key", "cosign_keyless"), required=True)
    consume.add_argument("--key-ref")
    consume.add_argument("--certificate-identity")
    consume.add_argument("--certificate-oidc-issuer")
    return cli


def main() -> None:
    args = parser().parse_args()
    try:
        export(args) if args.operation == "export" else import_archive(args)
    except (CatalogArchiveError, OSError, ValueError, KeyError) as error:
        raise SystemExit("application-catalog-airgap: closed preparation/import rejected") from error


if __name__ == "__main__":
    main()
