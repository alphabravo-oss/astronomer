#!/usr/bin/env python3
"""Hash the exact tracked plus non-ignored untracked source tree deterministically."""

import argparse
import hashlib
import json
import os
import stat
import subprocess
from pathlib import Path


def source_paths(root: Path) -> list[Path]:
    raw = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"], cwd=root,
    )
    return [Path(os.fsdecode(value)) for value in raw.split(b"\0") if value]


def tree_identity(root: Path, excluded: Path | None = None) -> dict[str, object]:
    root = root.resolve()
    excluded = excluded.resolve() if excluded else None
    digest = hashlib.sha256(b"astronomer-source-tree-v1\0")
    count = 0
    for relative in sorted(source_paths(root), key=lambda value: os.fsencode(value.as_posix())):
        path = root / relative
        try:
            resolved_parent = path.parent.resolve()
        except OSError:
            resolved_parent = path.parent
        if excluded and (path.resolve(strict=False) == excluded or excluded in path.resolve(strict=False).parents or
                         resolved_parent == excluded or excluded in resolved_parent.parents):
            continue
        encoded = os.fsencode(relative.as_posix())
        digest.update(len(encoded).to_bytes(8, "big")); digest.update(encoded)
        try:
            metadata = path.lstat()
        except FileNotFoundError:
            digest.update(b"missing\0")
            count += 1
            continue
        digest.update(stat.S_IMODE(metadata.st_mode).to_bytes(4, "big"))
        if stat.S_ISLNK(metadata.st_mode):
            payload = os.fsencode(os.readlink(path)); kind = b"symlink\0"
        elif stat.S_ISREG(metadata.st_mode):
            payload = path.read_bytes(); kind = b"regular\0"
        else:
            raise SystemExit(f"unsupported non-regular source path: {relative}")
        digest.update(kind); digest.update(len(payload).to_bytes(8, "big")); digest.update(payload)
        count += 1
    return {"schema_version": 1, "source_file_count": count, "source_tree_sha256": digest.hexdigest()}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path.cwd())
    parser.add_argument("--exclude", type=Path)
    args = parser.parse_args()
    print(json.dumps(tree_identity(args.root, args.exclude), sort_keys=True))


if __name__ == "__main__":
    main()
