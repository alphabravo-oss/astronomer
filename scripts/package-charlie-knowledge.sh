#!/usr/bin/env bash
# Source of truth: https://github.com/alphabravo-oss/astronomer-docs
# Package versioned Markdown under versions/<ver>
# into a single JSON document payload suitable for Charlie knowledge upload.
#
# This does NOT call Charlie APIs (needs credentials/collection IDs). It produces
# files you can upload + publish as product_version=<ver>.
#
# Usage:
#   ./scripts/package-charlie-knowledge.sh 0.3.5
#   OUT=/tmp/out ./scripts/package-charlie-knowledge.sh 0.3.5
set -euo pipefail
VERSION="${1:?product documentation version required, e.g. 0.3.5}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/docs/charlie-knowledge/versions/$VERSION"
OUT_DIR="${OUT:-$ROOT/docs/charlie-knowledge/dist}"
if [[ ! -d "$SRC" ]]; then
  echo "missing version tree: $SRC" >&2
  exit 1
fi
mkdir -p "$OUT_DIR"
MANIFEST="$OUT_DIR/astronomer-knowledge-$VERSION.manifest.txt"
COMBINED="$OUT_DIR/astronomer-knowledge-$VERSION.md"
: >"$MANIFEST"
: >"$COMBINED"
{
  echo "# Astronomer Charlie knowledge pack $VERSION"
  echo
  echo "Generated $(date -u +%Y-%m-%dT%H:%M:%SZ). Product version key: $VERSION"
  echo
} >>"$COMBINED"
while IFS= read -r -d '' f; do
  rel="${f#"$SRC/"}"
  echo "$rel" >>"$MANIFEST"
  {
    echo
    echo "---"
    echo
    echo "<!-- source: $rel -->"
    echo
    cat "$f"
    echo
  } >>"$COMBINED"
done < <(find "$SRC" -type f \( -name '*.md' -o -name '*.yaml' -o -name '*.yml' \) \
  ! -name '_meta.yaml' ! -name 'TEST-RUN.md' ! -name 'README.md' -print0 | sort -z)
# Also emit one JSON object per file for multi-document upload pipelines.
# Exclude operator checklists (TEST-RUN.md) from the RAG corpus — they are human-only.
python3 - "$SRC" "$OUT_DIR" "$VERSION" <<'PY'
import json, pathlib, sys
src, out, version = map(pathlib.Path, sys.argv[1:4])
skip_names = {"_meta.yaml", "TEST-RUN.md", "README.md"}
docs = []
for path in sorted(src.rglob("*")):
    if not path.is_file() or path.name in skip_names or path.name.startswith("_meta"):
        continue
    if path.suffix.lower() not in {".md", ".yaml", ".yml"}:
        continue
    rel = path.relative_to(src).as_posix()
    # metadata values must be strings (Charlie KnowledgeDocumentUpload)
    docs.append({
        "document_key": f"astronomer/{version}/{rel}",
        "title": path.stem.replace("-", " ").title(),
        "content_type": "text/markdown" if path.suffix.lower() == ".md" else "text/yaml",
        "metadata": {
            "product": "astronomer",
            "product_version": str(version),
            "path": rel,
            "kind": "charlie-knowledge",
            "maturity": "test-run",
        },
        "content": path.read_text(encoding="utf-8"),
    })
bundle = out / f"astronomer-knowledge-{version}.documents.json"
bundle.write_text(json.dumps(docs, indent=2), encoding="utf-8")
print(f"wrote {bundle} ({len(docs)} documents)")
print(f"product_version for PublishKnowledgeRelease / activate: {version}")
PY
echo "manifest: $MANIFEST"
echo "combined: $COMBINED"
echo "Next: upload documents to Charlie collection, publish release with product_version=$VERSION, activate, wait for index ready."
