# Charlie knowledge packaging workspace

The product documentation source of truth is
[`alphabravo-oss/astronomer-docs`](https://github.com/alphabravo-oss/astronomer-docs).
This directory intentionally contains no pre-v1 product corpus. Historical
0.3.x material is retained under `docs/archive/pre-v1/` for audit only and must
not be activated for an Astronomer v1 installation.

For a release, import the exact documentation tree whose product version
matches the Astronomer release, then package it deterministically:

```bash
# Copy the reviewed version directory from astronomer-docs first.
./scripts/package-charlie-knowledge.sh 1.0.0
```

The packaging script reads `docs/charlie-knowledge/versions/<version>` and
writes the artifact to `docs/charlie-knowledge/dist` unless `OUT` is set.
Release qualification must verify the manifest hash, upload the versioned
artifact through Charlie's configuration-only administration path, and activate
that exact product version. Never reuse a knowledge corpus from another product
version or silently fall back to an older one.
