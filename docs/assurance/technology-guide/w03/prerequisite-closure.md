# Exact prerequisite roles and capabilities

Distribution preparation requires asset input schema version 2, with an explicit
capability list and asset list. The signed BOM includes asset contract version 1.
Omitting these classifications cannot reproduce a qualified output through the
old category-only input. Historical v1 distribution artifacts are unaffected.
The schema is `deploy/bundles/schemas/distribution-asset-input-v2.schema.json`;
the bounded strict Go reader also enforces semantic role, member, subject and
capability closure. An empty capability list scopes a profile to its reviewed
builtin charts; it does not qualify platform prerequisites.

| Enabled capability | Mandatory exact asset roles |
| --- | --- |
| `database-cnpg` | CNPG operator, CNPG CRDs, PostgreSQL server, Barman cloud plugin, Barman cloud tools, object store, DR shell |
| `vulnerability-scanning` | Trivy database and Trivy Java database |
| `cache-valkey` | Valkey |
| `identity-dex` | Dex |
| `logs-fluent-bit` | Fluent Bit |
| `charlie` | Charlie |
| `application-catalog` | Application catalog |
| `distribution-registry` | Distribution registry |
| `platform-core` | All database/CNPG roles plus Valkey, Dex, Fluent Bit and Charlie |

Each role binds a fixed category and exact hashed member, with immutable OCI
subjects. The producer rejects changed reviewed member digests. Operator,
plugin, database and runtime manifests must classify exactly their declared
images, including generated CNPG `imageName` fields. Every runtime image uses
captured digest-bound OCI index, child manifest and configuration bytes for
linux/amd64 and linux/arm64. The consumer requires the exact image and workload
scope in the signed inventory; extra, duplicated or unclassified scopes fail.

Vulnerability databases are platform-independent artifacts. Their exact OCI
manifest bytes are included in the closed archive and bound to the subject
digest. A single payload layer must match the bundled database archive hash and
size, with no external layer URL. Effective scanner values derive the scanning
capability and must select those same database subjects, so leaving the
capability list empty cannot bypass database closure. This establishes content
identity, not database freshness or real Trivy operation; W04 owns those gates.

The producer does not install or take ownership of an operator. The assets
describe prerequisites that the selected platform profile requires; deployment
and operator ownership remain outside builtin delivery. No current full platform
profile is qualified merely because selected RustFS, Valkey or operator images
have upstream digest metadata. Actual supplier manifests, tool archives,
database payloads, chart values and selected integration evidence must close
before publication. Missing roles fail preparation and leave no qualified
layout; the affected supplier/profile is explicitly unavailable. Local QA role
fixtures are not vendor release evidence.

The [source-bound receipt](prerequisite-closure-evidence.json) records eight
passing Go packages with the actual selected Cosign producer enabled, seven
passing application archive cases, ten passing emitted release metadata cases
and two closed input schema fixtures. The chart-only archive fixture explicitly
disables vulnerability scanning; the separate scanner fixture includes actual
QA database subject-to-payload bindings. Missing exact roles, changed database
subjects/payloads, mutable inputs and omitted classification fields fail.

JSON ownership remains W03 distribution preparation: `artifact.go`'s existing
`buildLayout` BOM serializer now emits `distribution.BOM.Assets`,
`AssetContractVersion` and `Profile.Capabilities`, consumed by shared
`ReadClosedArchive`/`ReadArchive` strict Go readers. The reviewed input schema is
read by `buildLayout`; no new serializer call or public API/database schema is
introduced. The application exporter emits explicit empty asset/capability
classification for its separately scoped chart archive. W09 can update its exact
site inventory after integration without an opaque module bypass.

W03 remains active: actual publisher/mirror/pipeline wiring, runbook fixture
qualification and external supplier closure are still required. The current
host capacity preflight remains blocked at 61 GiB and 18% free against the
required 20 GiB and 20%; this does not become a passing release Make gate.
