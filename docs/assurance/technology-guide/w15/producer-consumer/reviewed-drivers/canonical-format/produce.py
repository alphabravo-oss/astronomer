"""Run the canonical producer into a new private external directory; no network."""
from __future__ import annotations

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sys


def main() -> None:
    if len(sys.argv) != 3:
        raise ValueError("exact source and fresh output roots required")
    os.umask(0o077)
    source, output = (Path(value).resolve() for value in sys.argv[1:])
    if output == source or output.is_relative_to(source) or output.exists():
        raise ValueError("producer output must be a new external private directory")
    sys.path.insert(0, str(source / "scripts"))
    spec = importlib.util.spec_from_file_location("selected_canonical_producer", source / "scripts/build_catalog.py")
    if spec is None or spec.loader is None:
        raise ValueError("canonical producer source unavailable")
    producer = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(producer)
    if producer.ROOT != source:
        raise ValueError("producer source root drift")
    producer.GENERATED = output
    # Use the canonical renderer and validators; only output ownership differs.
    files = producer.expected_files()
    output.mkdir(mode=0o700)
    for path, body in files.items():
        if not path.is_relative_to(output):
            raise ValueError("canonical producer escaped external output")
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with path.open("xb") as stream:
            stream.write(body)
    if not producer.check_outputs(files):
        raise ValueError("canonical generated-output verification failed")
    index = {
        str(path.relative_to(output)): {"bytes": len(body), "sha256": hashlib.sha256(body).hexdigest()}
        for path, body in sorted(files.items())
    }
    catalog = json.loads((output / "catalog.canonical.json").read_text())
    manifest = json.loads((output / "release-manifest.json").read_text())
    if manifest["indexDigest"] != "sha256:" + index["catalog.canonical.json"]["sha256"]:
        raise ValueError("release manifest document identity mismatch")
    if manifest["applicationCount"] != len(catalog["applications"]):
        raise ValueError("release manifest application cardinality mismatch")
    if manifest["repositoryCount"] != len(catalog["repositories"]):
        raise ValueError("release manifest repository cardinality mismatch")
    if set(manifest["entries"]) != {row["slug"] for row in catalog["applications"]}:
        raise ValueError("release manifest application graph incomplete")
    for row in catalog["applications"]:
        expected = "sha256:" + index["entries/" + row["slug"] + ".json"]["sha256"]
        if manifest["entries"][row["slug"]] != expected:
            raise ValueError("release manifest entry digest mismatch")
    print(json.dumps({"files": index, "applications": manifest["applicationCount"], "repositories": manifest["repositoryCount"]}, sort_keys=True))


if __name__ == "__main__":
    main()
