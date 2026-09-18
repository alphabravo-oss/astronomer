#!/usr/bin/env python3
"""Authenticated, versioned backup envelopes for Astronomer management DR.

The database dump and key bundle use streaming AES-256-GCM envelopes.  A
separate closed JSON manifest authenticates the object names, ciphertext
digests, release/source identity, and database schema version before restore.
"""

from __future__ import annotations

import argparse
import base64
import datetime as dt
import hashlib
import hmac
import json
import os
from pathlib import Path, PurePosixPath
import re
import secrets
import struct
import sys
import tarfile
import tempfile
from typing import Any, BinaryIO

from cryptography.exceptions import InvalidSignature, InvalidTag
from cryptography.hazmat.primitives import hashes, hmac as crypto_hmac
from cryptography.hazmat.primitives import padding
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
from cryptography.hazmat.primitives.kdf.hkdf import HKDF
from cryptography.hazmat.primitives.kdf.pbkdf2 import PBKDF2HMAC


MAGIC = b"ASTRODR1\n"
ENVELOPE_SCHEMA_VERSION = 1
MANIFEST_SCHEMA_VERSION = 1
ALGORITHM = "AES-256-GCM"
KDF = "PBKDF2-HMAC-SHA256"
KDF_ITERATIONS = 600_000
TAG_BYTES = 16
MAX_HEADER_BYTES = 64 * 1024
MAX_KEY_FILE_BYTES = 1024 * 1024
MAX_KEY_BUNDLE_BYTES = 16 * 1024 * 1024
CHUNK_BYTES = 1024 * 1024
LEGACY_CBC_KDF_ITERATIONS = 10_000
SHA256_RE = re.compile(r"^sha256:[a-f0-9]{64}$")
SECRET_KEY_NAME_RE = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$")


class DRError(ValueError):
    """An input or authentication contract was violated."""


def canonical_json(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8")


def b64(raw: bytes) -> str:
    return base64.b64encode(raw).decode("ascii")


def unb64(value: Any, field: str, expected_length: int | None = None) -> bytes:
    if not isinstance(value, str):
        raise DRError(f"{field} must be base64 text")
    try:
        raw = base64.b64decode(value, validate=True)
    except ValueError as exc:
        raise DRError(f"{field} is not valid base64") from exc
    if expected_length is not None and len(raw) != expected_length:
        raise DRError(f"{field} must decode to {expected_length} bytes")
    return raw


def require_regular_file(path: Path, label: str) -> None:
    if path.is_symlink() or not path.is_file():
        raise DRError(f"{label} must be a regular file, not a link")


def read_secret(path: Path) -> bytes:
    require_regular_file(path, "passphrase")
    secret = path.read_bytes().rstrip(b"\r\n")
    if len(secret) < 32:
        raise DRError("wrapping passphrase must contain at least 32 bytes")
    return secret


def derive_envelope_key(secret: bytes, salt: bytes, iterations: int) -> bytes:
    return PBKDF2HMAC(
        algorithm=hashes.SHA256(), length=32, salt=salt, iterations=iterations
    ).derive(secret)


def parse_context(raw: str) -> dict[str, str]:
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise DRError("context must be valid JSON") from exc
    if not isinstance(value, dict) or not value:
        raise DRError("context must be a non-empty object")
    if any(not isinstance(key, str) or not isinstance(item, str) or not item for key, item in value.items()):
        raise DRError("context keys and values must be non-empty strings")
    return value


def temporary_output(destination: Path) -> tuple[BinaryIO, Path]:
    if destination.exists() or destination.is_symlink():
        raise DRError(f"refusing to overwrite output {destination}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    handle = tempfile.NamedTemporaryFile(
        mode="w+b", prefix=f".{destination.name}.", dir=destination.parent, delete=False
    )
    os.chmod(handle.name, 0o600)
    return handle, Path(handle.name)


def commit_output(handle: BinaryIO, temporary: Path, destination: Path) -> None:
    handle.flush()
    os.fsync(handle.fileno())
    handle.close()
    os.replace(temporary, destination)


def encrypt_file(source: Path, destination: Path, passphrase: Path, purpose: str, context: dict[str, str]) -> None:
    require_regular_file(source, "plaintext input")
    secret = read_secret(passphrase)
    salt = secrets.token_bytes(16)
    nonce = secrets.token_bytes(12)
    header = {
        "algorithm": ALGORITHM,
        "context": context,
        "kdf": {"iterations": KDF_ITERATIONS, "name": KDF, "salt": b64(salt)},
        "nonce": b64(nonce),
        "purpose": purpose,
        "schema_version": ENVELOPE_SCHEMA_VERSION,
    }
    header_raw = canonical_json(header)
    key = derive_envelope_key(secret, salt, KDF_ITERATIONS)
    encryptor = Cipher(algorithms.AES(key), modes.GCM(nonce)).encryptor()
    encryptor.authenticate_additional_data(header_raw)
    output, temporary = temporary_output(destination)
    try:
        output.write(MAGIC)
        output.write(struct.pack(">I", len(header_raw)))
        output.write(header_raw)
        with source.open("rb") as plaintext:
            while chunk := plaintext.read(CHUNK_BYTES):
                output.write(encryptor.update(chunk))
        output.write(encryptor.finalize())
        output.write(encryptor.tag)
        commit_output(output, temporary, destination)
    except BaseException:
        output.close()
        temporary.unlink(missing_ok=True)
        raise


def parse_envelope_header(source: BinaryIO, size: int) -> tuple[dict[str, Any], bytes, int]:
    if source.read(len(MAGIC)) != MAGIC:
        raise DRError("unsupported backup envelope magic")
    length_raw = source.read(4)
    if len(length_raw) != 4:
        raise DRError("truncated backup envelope header")
    header_length = struct.unpack(">I", length_raw)[0]
    if header_length < 2 or header_length > MAX_HEADER_BYTES:
        raise DRError("backup envelope header length is invalid")
    header_raw = source.read(header_length)
    if len(header_raw) != header_length:
        raise DRError("truncated backup envelope header")
    try:
        header = json.loads(header_raw)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise DRError("backup envelope header is not valid JSON") from exc
    expected = {"algorithm", "context", "kdf", "nonce", "purpose", "schema_version"}
    if not isinstance(header, dict) or set(header) != expected:
        raise DRError("backup envelope header has an open or incomplete schema")
    if canonical_json(header) != header_raw:
        raise DRError("backup envelope header is not canonical JSON")
    if header["schema_version"] != ENVELOPE_SCHEMA_VERSION or header["algorithm"] != ALGORITHM:
        raise DRError("unsupported backup envelope version or algorithm")
    kdf = header["kdf"]
    if not isinstance(kdf, dict) or set(kdf) != {"iterations", "name", "salt"}:
        raise DRError("backup envelope KDF has an open or incomplete schema")
    if kdf["name"] != KDF or kdf["iterations"] != KDF_ITERATIONS:
        raise DRError("unsupported backup envelope KDF")
    if not isinstance(header["purpose"], str) or not header["purpose"]:
        raise DRError("backup envelope purpose is invalid")
    context = header["context"]
    if not isinstance(context, dict) or not context or any(
        not isinstance(key, str) or not isinstance(value, str) or not value
        for key, value in context.items()
    ):
        raise DRError("backup envelope context is invalid")
    prefix_length = len(MAGIC) + 4 + header_length
    if size < prefix_length + TAG_BYTES:
        raise DRError("truncated backup envelope ciphertext")
    return header, header_raw, prefix_length


def decrypt_file(
    source_path: Path,
    destination: Path,
    passphrase: Path,
    expected_purpose: str,
    expected_context: dict[str, str],
) -> None:
    require_regular_file(source_path, "encrypted input")
    size = source_path.stat().st_size
    secret = read_secret(passphrase)
    output, temporary = temporary_output(destination)
    try:
        with source_path.open("rb") as source:
            header, header_raw, prefix_length = parse_envelope_header(source, size)
            if header["purpose"] != expected_purpose:
                raise DRError("backup envelope purpose does not match the requested restore")
            if header["context"] != expected_context:
                raise DRError("backup envelope context does not match the requested restore")
            salt = unb64(header["kdf"]["salt"], "kdf.salt", 16)
            nonce = unb64(header["nonce"], "nonce", 12)
            source.seek(size - TAG_BYTES)
            tag = source.read(TAG_BYTES)
            source.seek(prefix_length)
            remaining = size - prefix_length - TAG_BYTES
            key = derive_envelope_key(secret, salt, KDF_ITERATIONS)
            decryptor = Cipher(algorithms.AES(key), modes.GCM(nonce, tag)).decryptor()
            decryptor.authenticate_additional_data(header_raw)
            while remaining:
                chunk = source.read(min(CHUNK_BYTES, remaining))
                if not chunk:
                    raise DRError("truncated backup envelope ciphertext")
                remaining -= len(chunk)
                output.write(decryptor.update(chunk))
            output.write(decryptor.finalize())
        commit_output(output, temporary, destination)
    except InvalidTag as exc:
        output.close()
        temporary.unlink(missing_ok=True)
        raise DRError("backup envelope authentication failed") from exc
    except BaseException:
        output.close()
        temporary.unlink(missing_ok=True)
        raise


def sha256_file(path: Path) -> str:
    require_regular_file(path, "manifest payload")
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while chunk := handle.read(CHUNK_BYTES):
            digest.update(chunk)
    return f"sha256:{digest.hexdigest()}"


def validate_object_key(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value or value.startswith("/") or "\\" in value:
        raise DRError(f"{field} is not a safe object key")
    path = PurePosixPath(value)
    if any(part in {"", ".", ".."} for part in path.parts) or any(character.isspace() for character in value):
        raise DRError(f"{field} is not a safe object key")
    return value


def manifest_auth_key(secret: bytes, salt: bytes) -> bytes:
    return HKDF(
        algorithm=hashes.SHA256(), length=32, salt=salt, info=b"astronomer-dr-manifest-v1"
    ).derive(secret)


def authenticate_manifest(document: dict[str, Any], secret: bytes) -> dict[str, Any]:
    salt = secrets.token_bytes(16)
    document["authentication"] = {
        "algorithm": "HMAC-SHA256",
        "key_derivation": "HKDF-SHA256",
        "salt": b64(salt),
    }
    signer = crypto_hmac.HMAC(manifest_auth_key(secret, salt), hashes.SHA256())
    signer.update(canonical_json(document))
    document["authentication"]["tag"] = b64(signer.finalize())
    return document


def payload_record(path: Path, object_key: str, purpose: str) -> dict[str, Any]:
    return {
        "envelope": {"algorithm": ALGORITHM, "purpose": purpose, "schema_version": ENVELOPE_SCHEMA_VERSION},
        "key": validate_object_key(object_key, "payload key"),
        "sha256": sha256_file(path),
        "size_bytes": path.stat().st_size,
    }


def create_manifest(args: argparse.Namespace) -> None:
    created_at = args.created_at
    if not created_at.endswith("Z") or "T" not in created_at:
        raise DRError("created-at must be a UTC RFC3339 timestamp")
    document = {
        "archive": payload_record(args.archive, args.archive_key, "database-dump"),
        "created_at": created_at,
        "key_bundle": payload_record(args.key_bundle, args.key_bundle_key, "key-bundle"),
        "schema_version": MANIFEST_SCHEMA_VERSION,
        "source": {
            "chart_version": args.chart_version,
            "database_schema_version": args.database_schema_version,
            "identity": args.source_identity,
            "release_name": args.release_name,
            "release_namespace": args.release_namespace,
        },
    }
    for field, value in document["source"].items():
        if field != "database_schema_version" and (not isinstance(value, str) or not value):
            raise DRError(f"source.{field} must be non-empty")
    authenticate_manifest(document, read_secret(args.passphrase_file))
    output, temporary = temporary_output(args.output)
    try:
        output.write(canonical_json(document) + b"\n")
        commit_output(output, temporary, args.output)
    except BaseException:
        output.close()
        temporary.unlink(missing_ok=True)
        raise


def load_manifest(path: Path) -> dict[str, Any]:
    require_regular_file(path, "backup manifest")
    try:
        document = json.loads(path.read_bytes())
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise DRError("backup manifest is not valid JSON") from exc
    expected = {"archive", "authentication", "created_at", "key_bundle", "schema_version", "source"}
    if not isinstance(document, dict) or set(document) != expected:
        raise DRError("backup manifest has an open or incomplete schema")
    if document["schema_version"] != MANIFEST_SCHEMA_VERSION:
        raise DRError("unsupported backup manifest schema")
    if not isinstance(document["created_at"], str):
        raise DRError("backup manifest created_at is invalid")
    source = document["source"]
    source_fields = {"chart_version", "database_schema_version", "identity", "release_name", "release_namespace"}
    if not isinstance(source, dict) or set(source) != source_fields:
        raise DRError("backup manifest source has an open or incomplete schema")
    if not isinstance(source["database_schema_version"], int) or source["database_schema_version"] < 1:
        raise DRError("backup manifest database schema version is invalid")
    if any(not isinstance(source[field], str) or not source[field] for field in source_fields - {"database_schema_version"}):
        raise DRError("backup manifest source identity is incomplete")
    payload_fields = {"envelope", "key", "sha256", "size_bytes"}
    envelope_fields = {"algorithm", "purpose", "schema_version"}
    for field, purpose in (("archive", "database-dump"), ("key_bundle", "key-bundle")):
        payload = document[field]
        if not isinstance(payload, dict) or set(payload) != payload_fields:
            raise DRError(f"backup manifest {field} has an open or incomplete schema")
        envelope = payload["envelope"]
        if not isinstance(envelope, dict) or set(envelope) != envelope_fields:
            raise DRError(f"backup manifest {field} envelope is invalid")
        if envelope != {"algorithm": ALGORITHM, "purpose": purpose, "schema_version": ENVELOPE_SCHEMA_VERSION}:
            raise DRError(f"backup manifest {field} envelope is unsupported")
        validate_object_key(payload["key"], f"{field}.key")
        if not isinstance(payload["sha256"], str) or SHA256_RE.fullmatch(payload["sha256"]) is None:
            raise DRError(f"backup manifest {field} digest is invalid")
        if not isinstance(payload["size_bytes"], int) or payload["size_bytes"] <= 0:
            raise DRError(f"backup manifest {field} size is invalid")
    authentication = document["authentication"]
    if not isinstance(authentication, dict) or set(authentication) != {"algorithm", "key_derivation", "salt", "tag"}:
        raise DRError("backup manifest authentication has an open or incomplete schema")
    if authentication["algorithm"] != "HMAC-SHA256" or authentication["key_derivation"] != "HKDF-SHA256":
        raise DRError("unsupported backup manifest authentication")
    return document


def verify_manifest(args: argparse.Namespace) -> None:
    document = load_manifest(args.input)
    authentication = document["authentication"]
    tag = unb64(authentication.pop("tag"), "authentication.tag", 32)
    salt = unb64(authentication["salt"], "authentication.salt", 16)
    verifier = crypto_hmac.HMAC(manifest_auth_key(read_secret(args.passphrase_file), salt), hashes.SHA256())
    verifier.update(canonical_json(document))
    try:
        verifier.verify(tag)
    except InvalidSignature as exc:
        raise DRError("backup manifest authentication failed") from exc
    authentication["tag"] = b64(tag)
    source = document["source"]
    for field, expected in (
        ("release_name", args.expect_release_name),
        ("release_namespace", args.expect_release_namespace),
        ("identity", args.expect_source_identity),
    ):
        if expected is not None and source[field] != expected:
            raise DRError(f"backup manifest source.{field} does not match this restore")
    if args.expect_prefix:
        prefix = args.expect_prefix.rstrip("/") + "/"
        if not document["archive"]["key"].startswith(prefix) or not document["key_bundle"]["key"].startswith(prefix):
            raise DRError("backup manifest object key is outside the expected prefix")
    for field, path in (("archive", args.archive_file), ("key_bundle", args.key_bundle_file)):
        if path is None:
            continue
        if path.stat().st_size != document[field]["size_bytes"] or sha256_file(path) != document[field]["sha256"]:
            raise DRError(f"backup manifest {field} payload digest/size mismatch")
    if args.print_field:
        value: Any = document
        for part in args.print_field.split("."):
            if not isinstance(value, dict) or part not in value:
                raise DRError("requested manifest field does not exist")
            value = value[part]
        if not isinstance(value, (str, int)):
            raise DRError("requested manifest field is not scalar")
        print(value)


def create_key_bundle(source_dir: Path, output_path: Path) -> None:
    if source_dir.is_symlink() or not source_dir.is_dir():
        raise DRError("key source must be a regular directory")
    entries = sorted(source_dir.iterdir(), key=lambda path: path.name)
    if not entries:
        raise DRError("key source directory is empty")
    names: set[str] = set()
    total = 0
    for entry in entries:
        if entry.is_symlink() or not entry.is_file() or SECRET_KEY_NAME_RE.fullmatch(entry.name) is None:
            raise DRError("key source may contain only top-level regular files")
        size = entry.stat().st_size
        if size <= 0 or size > MAX_KEY_FILE_BYTES:
            raise DRError(f"key file {entry.name} has an unsafe size")
        total += size
        names.add(entry.name)
    if total > MAX_KEY_BUNDLE_BYTES:
        raise DRError("key bundle exceeds the size limit")
    for required in {"SECRET_KEY", "ASTRONOMER_ENCRYPTION_KEY"}:
        if required not in names:
            raise DRError(f"key bundle is missing {required}")
    output, temporary = temporary_output(output_path)
    try:
        with tarfile.open(fileobj=output, mode="w|") as archive:
            for entry in entries:
                info = tarfile.TarInfo(entry.name)
                info.size = entry.stat().st_size
                info.mode = 0o600
                info.mtime = 0
                info.uid = info.gid = 0
                info.uname = info.gname = ""
                with entry.open("rb") as key_file:
                    archive.addfile(info, key_file)
        commit_output(output, temporary, output_path)
    except BaseException:
        output.close()
        temporary.unlink(missing_ok=True)
        raise


def extract_key_bundle(bundle: Path, destination: Path) -> None:
    require_regular_file(bundle, "key bundle")
    if destination.exists() or destination.is_symlink():
        raise DRError("refusing to overwrite key extraction directory")
    validated: list[tuple[tarfile.TarInfo, bytes]] = []
    names: set[str] = set()
    total = 0
    with tarfile.open(bundle, mode="r:") as archive:
        for member in archive:
            if not member.isfile() or member.name in names:
                raise DRError("key bundle contains a link, non-file, or duplicate member")
            path = PurePosixPath(member.name)
            if len(path.parts) != 1 or SECRET_KEY_NAME_RE.fullmatch(path.name) is None:
                raise DRError("key bundle contains an unsafe member path")
            if member.size <= 0 or member.size > MAX_KEY_FILE_BYTES:
                raise DRError("key bundle member has an unsafe size")
            handle = archive.extractfile(member)
            if handle is None:
                raise DRError("key bundle member cannot be read")
            content = handle.read(MAX_KEY_FILE_BYTES + 1)
            if len(content) != member.size:
                raise DRError("key bundle member size mismatch")
            total += len(content)
            if total > MAX_KEY_BUNDLE_BYTES:
                raise DRError("key bundle exceeds the size limit")
            names.add(member.name)
            validated.append((member, content))
    for required in {"SECRET_KEY", "ASTRONOMER_ENCRYPTION_KEY"}:
        if required not in names:
            raise DRError(f"key bundle is missing {required}")
    temporary = destination.with_name(f".{destination.name}.{secrets.token_hex(8)}")
    temporary.mkdir(mode=0o700, parents=False, exist_ok=False)
    try:
        for member, content in validated:
            target = temporary / member.name
            descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            with os.fdopen(descriptor, "wb") as handle:
                handle.write(content)
        os.replace(temporary, destination)
    except BaseException:
        for child in temporary.iterdir() if temporary.exists() else ():
            child.unlink(missing_ok=True)
        temporary.rmdir()
        raise


def legacy_decrypt_key_bundle(
    source_path: Path, destination: Path, passphrase: Path, sunset: dt.date
) -> None:
    """Read-only compatibility for pre-GA OpenSSL AES-CBC key bundles.

    CBC cannot authenticate its input. This command is therefore never used by
    the automated drill and refuses operation after the explicit sunset.
    """
    if dt.datetime.now(dt.timezone.utc).date() >= sunset:
        raise DRError(f"legacy AES-CBC compatibility expired on {sunset.isoformat()}")
    require_regular_file(source_path, "legacy key bundle")
    if source_path.stat().st_size > MAX_KEY_BUNDLE_BYTES + 32:
        raise DRError("legacy key bundle exceeds the size limit")
    raw = source_path.read_bytes()
    if len(raw) < 32 or raw[:8] != b"Salted__" or (len(raw) - 16) % 16 != 0:
        raise DRError("legacy key bundle is not an OpenSSL salted AES-CBC envelope")
    material = PBKDF2HMAC(
        algorithm=hashes.SHA256(),
        length=48,
        salt=raw[8:16],
        iterations=LEGACY_CBC_KDF_ITERATIONS,
    ).derive(read_secret(passphrase))
    decryptor = Cipher(algorithms.AES(material[:32]), modes.CBC(material[32:])).decryptor()
    unpadder = padding.PKCS7(128).unpadder()
    output, temporary = temporary_output(destination)
    try:
        plaintext = decryptor.update(raw[16:]) + decryptor.finalize()
        output.write(unpadder.update(plaintext) + unpadder.finalize())
        commit_output(output, temporary, destination)
        print(
            "dr-crypto: WARNING: legacy AES-CBC input is unauthenticated; "
            "extract only with key-bundle-extract in an isolated environment",
            file=sys.stderr,
        )
    except ValueError as exc:
        output.close()
        temporary.unlink(missing_ok=True)
        raise DRError("legacy key bundle padding is invalid") from exc
    except BaseException:
        output.close()
        temporary.unlink(missing_ok=True)
        raise


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser(description=__doc__)
    subcommands = result.add_subparsers(dest="command", required=True)

    encrypt = subcommands.add_parser("encrypt")
    encrypt.add_argument("--input", type=Path, required=True)
    encrypt.add_argument("--output", type=Path, required=True)
    encrypt.add_argument("--passphrase-file", type=Path, required=True)
    encrypt.add_argument("--purpose", choices=("database-dump", "key-bundle"), required=True)
    encrypt.add_argument("--context", required=True)

    decrypt = subcommands.add_parser("decrypt")
    decrypt.add_argument("--input", type=Path, required=True)
    decrypt.add_argument("--output", type=Path, required=True)
    decrypt.add_argument("--passphrase-file", type=Path, required=True)
    decrypt.add_argument("--expect-purpose", choices=("database-dump", "key-bundle"), required=True)
    decrypt.add_argument("--expect-context", required=True)

    create = subcommands.add_parser("manifest-create")
    create.add_argument("--archive", type=Path, required=True)
    create.add_argument("--archive-key", required=True)
    create.add_argument("--key-bundle", type=Path, required=True)
    create.add_argument("--key-bundle-key", required=True)
    create.add_argument("--passphrase-file", type=Path, required=True)
    create.add_argument("--release-name", required=True)
    create.add_argument("--release-namespace", required=True)
    create.add_argument("--chart-version", required=True)
    create.add_argument("--source-identity", required=True)
    create.add_argument("--database-schema-version", type=int, required=True)
    create.add_argument("--created-at", required=True)
    create.add_argument("--output", type=Path, required=True)

    verify = subcommands.add_parser("manifest-verify")
    verify.add_argument("--input", type=Path, required=True)
    verify.add_argument("--passphrase-file", type=Path, required=True)
    verify.add_argument("--archive-file", type=Path)
    verify.add_argument("--key-bundle-file", type=Path)
    verify.add_argument("--expect-release-name")
    verify.add_argument("--expect-release-namespace")
    verify.add_argument("--expect-source-identity")
    verify.add_argument("--expect-prefix")
    verify.add_argument("--print-field")

    bundle = subcommands.add_parser("key-bundle-create")
    bundle.add_argument("--source", type=Path, required=True)
    bundle.add_argument("--output", type=Path, required=True)

    extract = subcommands.add_parser("key-bundle-extract")
    extract.add_argument("--input", type=Path, required=True)
    extract.add_argument("--output", type=Path, required=True)

    legacy = subcommands.add_parser("legacy-key-bundle-decrypt")
    legacy.add_argument("--input", type=Path, required=True)
    legacy.add_argument("--output", type=Path, required=True)
    legacy.add_argument("--passphrase-file", type=Path, required=True)
    legacy.add_argument("--sunset", type=dt.date.fromisoformat, required=True)
    return result


def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    try:
        if args.command == "encrypt":
            encrypt_file(args.input, args.output, args.passphrase_file, args.purpose, parse_context(args.context))
        elif args.command == "decrypt":
            decrypt_file(args.input, args.output, args.passphrase_file, args.expect_purpose, parse_context(args.expect_context))
        elif args.command == "manifest-create":
            create_manifest(args)
        elif args.command == "manifest-verify":
            verify_manifest(args)
        elif args.command == "key-bundle-create":
            create_key_bundle(args.source, args.output)
        elif args.command == "key-bundle-extract":
            extract_key_bundle(args.input, args.output)
        elif args.command == "legacy-key-bundle-decrypt":
            legacy_decrypt_key_bundle(args.input, args.output, args.passphrase_file, args.sunset)
        else:  # pragma: no cover - argparse owns this boundary.
            raise DRError("unknown command")
    except (DRError, OSError, tarfile.TarError) as exc:
        print(f"dr-crypto: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
