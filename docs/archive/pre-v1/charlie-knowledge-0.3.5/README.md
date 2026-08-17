# Astronomer Charlie knowledge (local mirror)

**Source of truth:** [`alphabravo-oss/astronomer-docs`](https://github.com/alphabravo-oss/astronomer-docs)

| Product version | Docs | Release | Test-run notes |
| --- | --- | --- | --- |
| **0.3.5** | [versions/0.3.5](https://github.com/alphabravo-oss/astronomer-docs/tree/main/versions/0.3.5) | [v0.3.5](https://github.com/alphabravo-oss/astronomer-docs/releases/tag/v0.3.5) | [TEST-RUN.md](https://github.com/alphabravo-oss/astronomer-docs/blob/main/versions/0.3.5/TEST-RUN.md) |

Prefer PRs against **astronomer-docs**. This directory may hold a working copy for offline packaging.

```bash
# From astronomer-docs clone:
./scripts/package-charlie-knowledge.sh 0.3.5
CHARLIE_API_KEY=… ./scripts/publish-to-charlie.sh 0.3.5   # config_admin only
```

Charlie needs `product_version=0.3.5` activated on the product collection after upload.
