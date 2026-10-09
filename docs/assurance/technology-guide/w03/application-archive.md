# Authenticated application chart/profile transfer

The application archive exporter now accepts exact reviewed chart archives and
profile values, renders them offline with the same renderer used for builtin
distribution preparation, and binds the original catalog, every chart archive,
coalesced values and complete image inventory into a closed BOM. Captured OCI
platform evidence must cover both linux/amd64 and linux/arm64. Mutable images,
missing recursive dependencies, changed archives and uncovered applications fail
preparation. Kubernetes version major/minor fields are parsed consistently with
the complete version; a fixture exercises version-dependent chart rendering.

The external transfer document binds the compressed archive, embedded BOM,
catalog and release. The canonical in-process verifier authenticates that
document against caller-mounted trust before opening the archive. Import then
uses the shared bounded, closed distribution archive reader, checks exact values
and inventory, and rerenders each imported chart. Only after those checks does
the importer invoke the existing safe airgap-kit extractor. Catalog URLs and
publisher document/signature bytes remain unchanged. No import builds or
downloads a verifier, fetches trust, or silently rewrites a catalog URL.

The [source-bound evidence](application-archive-evidence.json) records seven
passing Python fixtures with actual official Cosign 3.1.3 signing and canonical
verification, plus six passing Go packages. An ephemeral local QA identity is
distinct from Astronomer's release OIDC publisher. A working public proxy
negative control precedes each fixture; signing and verification make zero
requests to that proxy. Authentic signatures over unknown/missing transfer
fields and extra, duplicate, link or traversal archive members still fail.
Closed schema fixtures validate real emitted reports and reject unknown,
missing and incorrectly typed fields. Go readers additionally enforce bounded
input sizes, strict duplicate/depth/single-document JSON and semantic bindings.

`make release-contract-check` stopped at the unchanged host capacity preflight:
60 GiB available, 18% free, against the required 20 GiB and 20%. Its downstream
checks did not run. This is not a passing Make gate.

Import reports verification and inventory completion for the reviewed profiles,
while explicitly reporting runtime qualification unavailable until a signed
local OCI catalog and immutable child subjects are qualified. The runtime's
disconnected empty-catalog configuration remains disabled and makes no public
fallback request. This archive does not enable a mutable HTTP catalog. Published
supplier assets, release pipeline execution and fresh-cache no-egress cluster
qualification remain separate outstanding conditions.

## JSON serializer ownership

These preparation-only formats are owned by W03 distribution preparation.
They do not alter public API or database JSON formats. The inventory below is
for coordination with W09's serializer governance inventory after integration.

| Serializer | Schema | Reader |
| --- | --- | --- |
| `deploy/bundles/cmd/distribution/main.go`, application profile output branch; `application.go` profile size accounting | `deploy/bundles/schemas/application-profile-report-v1.schema.json` | `scripts/application-catalog-airgap.py` exporter and Go `verifyApplicationArchive` per-profile report reader |
| Operator-supplied reviewed profile index | `deploy/bundles/schemas/application-profile-input-v1.schema.json` | Go `renderApplicationProfiles`, strict decoder plus chart/catalog/render/platform checks |
| `scripts/application-catalog-airgap.py` external transfer document | `deploy/bundles/schemas/application-transfer-v1.schema.json` | Go `verifyApplicationArchive`, after caller-pinned signature authentication |
| `scripts/application-catalog-airgap.py` final import status | `deploy/bundles/schemas/application-import-status-v1.schema.json` | Operator command output; fixture validates exact emitted shape |
| `scripts/application-catalog-airgap.py` embedded `bom.json`, values and image inventories | Existing `distribution.BOM`, profile object and `distribution.Inventory` contracts | Shared `ReadClosedArchive`, strict profile values/inventory and offline rerender reader |

All closed top-level report/transfer/status structures reject unrecognized
fields. Chart values intentionally remain an object controlled by the reviewed
chart schema and render; signature and exact archive/profile bindings cover
their contents. Source hashes in the evidence exclude the evidence document
itself; no unsigned evidence JSON asserts authoritative package completion.
