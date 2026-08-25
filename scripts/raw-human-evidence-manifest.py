#!/usr/bin/env python3
"""Build or verify a closed digest manifest for retained human-study files."""

import argparse
import hashlib
import json
from pathlib import Path


def files(root: Path, manifest: Path) -> list[dict]:
    bundle = manifest.with_name(f"{manifest.stem}.sigstore.json")
    rows = []
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ValueError("raw human evidence cannot contain symlinks")
        if not path.is_file() or path in {manifest, bundle}:
            continue
        rel = path.relative_to(root).as_posix()
        rows.append({"path": rel, "sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "size": path.stat().st_size})
    return rows


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    if not args.root.is_dir() or args.root.is_symlink() or args.manifest.parent.resolve() != args.root.resolve(): raise ValueError("manifest must be inside a regular evidence root")
    observed = {"schema_version": 1, "files": files(args.root, args.manifest)}
    if not observed["files"]: raise ValueError("raw human evidence is empty")
    if args.verify:
        if json.loads(args.manifest.read_text(encoding="utf-8")) != observed: raise ValueError("raw human evidence manifest mismatch")
    else:
        if args.manifest.exists(): raise ValueError("refusing to overwrite raw human evidence manifest")
        args.manifest.write_text(json.dumps(observed, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")


if __name__ == "__main__": main()
