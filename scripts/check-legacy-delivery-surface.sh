#!/usr/bin/env bash
# Inventory or reject Argo, legacy fleet-operation, and Rancher Fleet surfaces.
#
# --report is the Wave 0 characterization mode: it always reports findings and
# exits zero unless the scan itself is invalid/incomplete. --fail is the v1
# release mode: active runtime or built-artifact findings exit one. Historical
# decision/migration context is counted separately and never silently promoted
# into an active allowlist.

set -euo pipefail

SCRIPT_PATH="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
exec python3 - "$SCRIPT_PATH" "$@" <<'PY'
from __future__ import annotations

import argparse
import io
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from typing import Iterable


PATTERNS = {
    "banned_rancher_fleet": re.compile(
        rb"(?:fleet\.cattle\.io|github\.com/rancher/fleet|ghcr\.io/rancher/fleet|quay\.io/rancher/fleet)",
        re.IGNORECASE,
    ),
    "legacy_argo": re.compile(
        rb"(?:argocd|argo[-_ ]?cd|argoproj\.io|\bApplicationSet\b)",
        re.IGNORECASE,
    ),
    "legacy_fleet_operations": re.compile(
        rb"(?:fleet_operations?|fleet_operation_targets?|fleet[-_](?:orchestrate|selector)|agent_fleet|\bFleetOperation\w*|[\"'/]fleet(?:[\"'/\s]|$))",
        re.IGNORECASE,
    ),
}

TEXT_SUFFIXES = {
    ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".json",
    ".yaml", ".yml", ".md", ".txt", ".tpl", ".html", ".css", ".scss",
    ".sql", ".sh", ".py", ".toml", ".lock", ".mod", ".sum",
}
TEXT_NAMES = {"Dockerfile", "Makefile", "LICENSE", "NOTICE"}
ARCHIVE_SUFFIXES = {".tgz", ".tar", ".gz"}
MAX_FILE_BYTES = 64 * 1024 * 1024
MAX_BUILT_ARTIFACT_BYTES = 512 * 1024 * 1024

# Exact, reviewable development allowlist. These paths contain historical
# explanation required to execute the deletion safely. They are still reported.
HISTORICAL_PREFIXES = (
    "advisor-plans/",
    "plans/",
    "docs/archive/",
    "docs/plans/",
    "docs/architecture/decisions/",
    "docs/assurance/flux-migration/",
)
HISTORICAL_FILES = {
    "docs/argo-owns-it-all-plan.md",
    "docs/argocd-fleet-equivalence.md",
    "docs/control-plane-state-contract.md",
    "docs/rancher-astronomer-comparison.md",
    "docs/rancher-quality-phase0-code-health-inventory.md",
    "docs/rancher-quality-phase0-inventory.md",
    "docs/rancher-quality-phase0-operation-task-inventory.md",
    "docs/rancher-quality-phase0-quality-backlog.md",
    "docs/rancher-quality-phase0-surface-inventory.md",
    "docs/rancher-quality-phase0-system-inventories.md",
}
MIGRATION_PREFIXES = ("internal/db/migrations/",)
IGNORED_PARTS = {
    ".git", ".claude", ".next", ".turbo", "node_modules", "vendor",
    ".cache", ".idea", ".vscode", "coverage",
}


class ScanError(RuntimeError):
    pass


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="check-legacy-delivery-surface.sh",
        description="Inventory or reject legacy delivery-engine identifiers without printing source contents.",
    )
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--report", action="store_true", help="report findings without rejecting the legacy baseline")
    mode.add_argument("--fail", action="store_true", help="reject active runtime and built-artifact findings")
    parser.add_argument("--root", type=Path, help="repository root (default: parent of this script)")
    parser.add_argument("--image", action="append", default=[], help="built image to stream-scan; repeatable")
    parser.add_argument("--output", type=Path, help="write the deterministic report to this path")
    parser.add_argument(
        "--skip-generated-surfaces",
        action="store_true",
        help="skip Helm render and CLI help generation (fixtures only)",
    )
    parser.add_argument("script_path", type=Path, help=argparse.SUPPRESS)
    return parser.parse_args()


def relative(path: Path, root: Path) -> str:
    return path.resolve().relative_to(root.resolve()).as_posix()


def classify(path: str) -> str:
    if path.startswith(MIGRATION_PREFIXES):
        return "migration_context"
    if path in HISTORICAL_FILES or path.startswith(HISTORICAL_PREFIXES):
        return "historical_allowlist"
    if path.startswith(("frontend/dist/", "dist/", "bin/", "build/")):
        return "built_artifact"
    return "active_runtime"


def is_candidate(path: Path) -> bool:
    if path.name in TEXT_NAMES or path.suffix.lower() in TEXT_SUFFIXES:
        return True
    return path.suffix.lower() in ARCHIVE_SUFFIXES


def scan_bytes(data: bytes) -> dict[str, int]:
    return {name: len(regex.findall(data)) for name, regex in PATTERNS.items()}


def add_counts(findings: dict[tuple[str, str, str], int], scope: str, path: str, data: bytes) -> None:
    for signature, count in scan_bytes(data).items():
        if count:
            findings[(scope, signature, path)] = findings.get((scope, signature, path), 0) + count


def scan_streaming_file(
    findings: dict[tuple[str, str, str], int], scope: str, path: str, file_path: Path
) -> None:
    """Scan a bounded built binary without retaining it in memory."""
    with file_path.open("rb") as handle:
        add_stream_counts(findings, scope, path, handle)


def add_stream_counts(
    findings: dict[tuple[str, str, str], int], scope: str, path: str, handle: io.BufferedReader
) -> None:
    counts = {name: 0 for name in PATTERNS}
    tail = b""
    while chunk := handle.read(1024 * 1024):
        data = tail + chunk
        boundary = len(tail)
        for name, regex in PATTERNS.items():
            counts[name] += sum(1 for match in regex.finditer(data) if match.end() > boundary)
        tail = data[-128:]
    for signature, count in counts.items():
        if count:
            findings[(scope, signature, path)] = count


def scan_archive(
    archive_path: Path,
    repo_path: str,
    scope: str,
    findings: dict[tuple[str, str, str], int],
) -> None:
    try:
        with tarfile.open(archive_path, mode="r:*") as archive:
            for member in archive.getmembers():
                if not member.isfile() or member.size > MAX_FILE_BYTES:
                    continue
                member_path = PurePosixPath(member.name)
                if any(part in IGNORED_PARTS for part in member_path.parts):
                    continue
                if member_path.suffix.lower() not in TEXT_SUFFIXES and member_path.name not in TEXT_NAMES:
                    continue
                handle = archive.extractfile(member)
                if handle is not None:
                    add_counts(findings, scope, f"{repo_path}!/{member_path}", handle.read(MAX_FILE_BYTES + 1))
    except (tarfile.TarError, OSError) as exc:
        raise ScanError(f"cannot inspect archive {repo_path}: {exc}") from exc


def scan_tree(root: Path, self_path: Path) -> tuple[dict[tuple[str, str, str], int], int]:
    findings: dict[tuple[str, str, str], int] = {}
    scanned = 0
    self_resolved = self_path.resolve()
    candidates: list[Path] = []
    for current, directories, files in os.walk(root, topdown=True, followlinks=False):
        directories[:] = sorted(item for item in directories if item not in IGNORED_PARTS)
        candidates.extend(Path(current) / item for item in sorted(files))
    for path in candidates:
        if not path.is_file() or path.is_symlink():
            continue
        repo_path = relative(path, root)
        built_candidate = repo_path.startswith(("frontend/dist/", "dist/", "bin/", "build/"))
        if path.resolve() == self_resolved or (not built_candidate and not is_candidate(path)):
            continue
        # Test fixtures exercise the scanner but are not a product surface.
        if repo_path.startswith("scripts/testdata/legacy-delivery/") or repo_path == "scripts/tests/check-legacy-delivery-surface_test.sh":
            continue
        scope = classify(repo_path)
        if path.suffix.lower() in ARCHIVE_SUFFIXES:
            scan_archive(path, repo_path, scope, findings)
            scanned += 1
            continue
        try:
            size = path.stat().st_size
            if scope == "built_artifact" and size <= MAX_BUILT_ARTIFACT_BYTES:
                scan_streaming_file(findings, scope, repo_path, path)
                scanned += 1
                continue
            if size > MAX_FILE_BYTES:
                continue
            add_counts(findings, scope, repo_path, path.read_bytes())
            scanned += 1
        except OSError as exc:
            raise ScanError(f"cannot scan {repo_path}: {exc}") from exc
    return findings, scanned


def merge(findings: dict[tuple[str, str, str], int], other: dict[tuple[str, str, str], int]) -> None:
    for key, count in other.items():
        findings[key] = findings.get(key, 0) + count


def scan_generated(root: Path) -> tuple[dict[tuple[str, str, str], int], list[str]]:
    findings: dict[tuple[str, str, str], int] = {}
    notes: list[str] = []
    chart = root / "deploy/chart"
    helm = shutil.which("helm")
    if chart.is_dir():
        if helm is None:
            raise ScanError("Helm is required to scan the rendered chart")
        result = subprocess.run(
            [
                helm,
                "template",
                "legacy-surface-scan",
                str(chart),
                "--set",
                "secrets.secretKey=legacy-surface-render-signing-key",
                "--set",
                "secrets.encryptionKey=I2oWSIt6LO68xR6lxhqBpQxhesPuii5R6ubog-Id-yo=",
            ],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=120,
            check=False,
        )
        if result.returncode != 0:
            raise ScanError(f"Helm render failed with exit {result.returncode}")
        add_counts(findings, "generated_chart", "@helm-render", result.stdout)
        notes.append("generated_chart=helm_template")
    else:
        notes.append("generated_chart=not_present")

    cli = root / "cmd/astro"
    if cli.is_dir() and (root / "go.mod").is_file():
        go = shutil.which("go")
        if go is None:
            raise ScanError("Go is required to scan generated CLI help")
        result = subprocess.run(
            [go, "run", "./cmd/astro", "--help"],
            cwd=root,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=180,
            check=False,
            env={**os.environ, "CGO_ENABLED": "0"},
        )
        if result.returncode != 0:
            raise ScanError(f"CLI help generation failed with exit {result.returncode}")
        add_counts(findings, "generated_cli", "@astro-help", result.stdout + result.stderr)
        notes.append("generated_cli=go_run")
    else:
        notes.append("generated_cli=not_present")
    return findings, notes


def scan_image(image: str, ordinal: int) -> dict[tuple[str, str, str], int]:
    docker = shutil.which("docker")
    strings = shutil.which("strings")
    if docker is None or strings is None:
        raise ScanError("Docker and strings are required when --image is used")
    inspect = subprocess.run(
        [docker, "image", "inspect", image], stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False, timeout=60
    )
    if inspect.returncode != 0:
        raise ScanError(f"built image {ordinal} is unavailable")
    findings: dict[tuple[str, str, str], int] = {}
    add_counts(findings, "built_image", f"@image-{ordinal}-config", inspect.stdout)

    created = subprocess.run(
        [docker, "create", image], stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False, timeout=60
    )
    if created.returncode != 0:
        raise ScanError(f"cannot create scan container for built image {ordinal}")
    container = created.stdout.decode("utf-8", "replace").strip()
    try:
        export = subprocess.Popen(
            [docker, "export", container], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL
        )
        assert export.stdout is not None
        extract_strings = subprocess.Popen(
            [strings, "-a"],
            stdin=export.stdout,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )
        export.stdout.close()
        assert extract_strings.stdout is not None
        add_stream_counts(
            findings,
            "built_image",
            f"@image-{ordinal}-filesystem",
            extract_strings.stdout,
        )
        extract_strings.stdout.close()
        strings_status = extract_strings.wait(timeout=300)
        export_status = export.wait(timeout=30)
        if export_status != 0 or strings_status != 0:
            raise ScanError(
                f"cannot stream-scan built image {ordinal} "
                f"(docker={export_status}, strings={strings_status})"
            )
    finally:
        subprocess.run([docker, "rm", "-f", container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    return findings


def render_report(
    mode: str,
    scanned_files: int,
    findings: dict[tuple[str, str, str], int],
    notes: Iterable[str],
    image_count: int,
) -> str:
    scopes = (
        "active_runtime", "historical_allowlist", "migration_context", "built_artifact",
        "generated_chart", "generated_cli", "built_image",
    )
    lines = [
        "legacy_delivery_surface_report_version=1",
        f"mode={mode}",
        f"source_files_scanned={scanned_files}",
        f"built_images_scanned={image_count}",
        "source_contents_emitted=false",
        "rancher_fleet_runtime_policy=prohibited",
        "historical_allowlist_is_runtime_authorization=false",
        *notes,
    ]
    for scope in scopes:
        for signature in PATTERNS:
            paths = sorted({path for item_scope, item_signature, path in findings if item_scope == scope and item_signature == signature})
            matches = sum(
                count
                for (item_scope, item_signature, _), count in findings.items()
                if item_scope == scope and item_signature == signature
            )
            lines.append(f"summary scope={scope} signature={signature} files={len(paths)} matches={matches}")
            for path in paths:
                lines.append(
                    f"surface scope={scope} signature={signature} path={path} matches={findings[(scope, signature, path)]}"
                )
    active_scopes = {"active_runtime", "built_artifact", "generated_chart", "generated_cli", "built_image"}
    prohibited = sum(count for (scope, _, _), count in findings.items() if scope in active_scopes)
    banned = sum(
        count
        for (scope, signature, _), count in findings.items()
        if scope in active_scopes and signature == "banned_rancher_fleet"
    )
    lines.append(f"active_prohibited_matches={prohibited}")
    lines.append(f"active_rancher_fleet_matches={banned}")
    lines.append("verdict=fail" if mode == "fail" and prohibited else "verdict=pass")
    return "\n".join(lines) + "\n"


def main() -> int:
    args = parse_args()
    script_path = args.script_path.resolve()
    root = (args.root or script_path.parent.parent).resolve()
    if not root.is_dir():
        print(f"legacy delivery surface: ERROR: root is not a directory: {root}", file=sys.stderr)
        return 2
    try:
        findings, scanned = scan_tree(root, script_path)
        notes: list[str] = []
        if args.skip_generated_surfaces:
            notes.extend(("generated_chart=skipped", "generated_cli=skipped"))
        else:
            generated, generated_notes = scan_generated(root)
            merge(findings, generated)
            notes.extend(generated_notes)
        image_refs = list(args.image)
        env_images = os.environ.get("ASTRONOMER_LEGACY_SCAN_IMAGES", "")
        if env_images:
            image_refs.extend(item.strip() for item in env_images.split(",") if item.strip())
        for ordinal, image in enumerate(image_refs, start=1):
            merge(findings, scan_image(image, ordinal))
        mode = "report" if args.report else "fail"
        report = render_report(mode, scanned, findings, notes, len(image_refs))
        if args.output:
            output = args.output if args.output.is_absolute() else root / args.output
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_text(report, encoding="utf-8")
        sys.stdout.write(report)
        active_scopes = {"active_runtime", "built_artifact", "generated_chart", "generated_cli", "built_image"}
        prohibited = any(scope in active_scopes for scope, _, _ in findings)
        return 1 if args.fail and prohibited else 0
    except (ScanError, subprocess.TimeoutExpired) as exc:
        print(f"legacy delivery surface: ERROR: {exc}", file=sys.stderr)
        return 2


raise SystemExit(main())
PY
