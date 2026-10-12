#!/usr/bin/env python3
"""Build every discovered first-party Go command and retain binary identities."""
import hashlib
import json
import pathlib
import subprocess

root = pathlib.Path(__file__).resolve().parent.parent
output = root / ".artifacts/toolchain/bin"
output.mkdir(parents=True, exist_ok=True)
module = subprocess.check_output(["go", "list", "-m"], cwd=root, text=True).strip()
packages = subprocess.check_output(["go", "list", "-f", '{{if eq .Name "main"}}{{.ImportPath}}{{end}}', "./..."], cwd=root, text=True).splitlines()
binaries = []
for package in sorted(filter(None, packages)):
    relative = package.removeprefix(module + "/")
    target = output / relative.replace("/", "-")
    subprocess.run(["go", "build", "-trimpath", "-o", str(target), package], cwd=root, check=True)
    binaries.append({"package": relative, "digest": "sha256:" + hashlib.sha256(target.read_bytes()).hexdigest(), "buildMetadata": subprocess.check_output(["go", "version", "-m", str(target)], text=True).replace(str(target), relative)})
manifest = {"schemaVersion": 1, "goVersion": subprocess.check_output(["go", "version"], text=True).strip(), "sourceCommit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(), "binaries": binaries}
(output.parent / "first-party-builds.json").write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
print(f"Built {len(binaries)} first-party binaries, including command and qualification helpers")
