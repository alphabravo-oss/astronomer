# Compatibility surfaces captured before modernization

Input identity is `6c3da188`; source-input SHA256s in [components.json](components.json) bind the current machine contracts. No contract is changed by this capture.

| Surface | Canonical input / consumers | Preserved behavior / next review |
| --- | --- | --- |
| REST API | `docs/openapi.yaml`; embedded server spec, generated browser operations/wire types, Go SDK | v1 auth/error/tenant/operation/list contracts; regenerate all consumers from canonical spec, assess diff in W06/W07/W09 |
| Dynamic/streaming APIs | Bounded Kubernetes proxy adapters, authenticated WebSocket/SSE, terminal/tunnel | Dynamic upstream payloads remain explicit exceptions, not falsely closed OpenAPI DTOs; W05 cancellation preserves durable mutation ownership |
| Browser auth | HttpOnly access/refresh cookies, CSRF, shared refresh, revoked families | Server rotation/replay checks already exist; W05 bounds shared refresh without tying its lifetime to one caller |
| Go SDK / CLI | `pkg/astroclient/astroclient.gen.go` generated client, `cmd/astro` Cobra tree, `docs/cli.md` | Existing commands/flags/errors remain public; cli-docs-check derives from real command tree |
| Agent compatibility | `deploy/release/compatibility.yaml` agent and agent_protocol; model schemas/strict decoders | Delivery protocol 2 and required v2 capabilities, tunnel 1, heartbeat 2; agent support 1.1–<2 with minimum compatible 1.0; future majors rejected |
| Assignment/status | Bounded cluster-bound snapshots, generation fences, deterministic materialization/checkpoints/tombstones | Stale sessions cannot revive deleted intent; disconnect retains last generation; no remote kubeconfig executor |
| Charlie bridge | Pinned bridge spec/schema/authority/pin/conformance, generated wire/client, release artifact | Version negotiation, action authorization/audit, findings and credential redaction remain; source pins are not fresh external qualification |
| Configuration/chart | Typed Config/defaults, generated configuration.md/.env.example, chart values/schema/templates | Explicit database/CNPG/Valkey modes, Secret ownership, trust roots/mirror policy. Preserve existing overrides through reviewed migration |
| Database | Canonical queries/generated sqlc, migrations 001–071, compatibility PostgreSQL 16/17 | Target 71, reversible floor 66, supported upgrade 1.1.x/1.2.x; Helm rollback never automatically runs down migrations |
| Durable JSON | Migration 049 durable schema registry/trigger/view, writer/reader DTOs; assignments, tasks, rollout/placement, inhibition/audit/operation documents | Versioned reader policy; broad current shape validation does not prove nested validity. W09 inventories existing records before constraints |
| Credentials/ciphertext | `internal/auth/crypto.go`, encrypted DB columns, `cmd/keyrotate`, key refs and backup envelopes | Fernet algorithm/key-ID/version envelope, multi-key and legacy reads; keys/backups must remain decryptable; no new algorithm without migration |
| Source/trust | Compatibility git commit, OCI digest, Helm HTTP version+SHA256, Helm OCI manifest digest; release/Flux/catalog signed identities | Mirror changes location, never content identity; preserve vendor signing-time trust and scoped SSRF policy; W03 fixes consumed catalog/import paths |
| Runtime/image release | Release manifest schema, seven application roles, artifact/SBOM/provenance/signatures, mirror map | Fresh-and-upgrade 1.2.x contract; v1.2.0/v1.2.1 bytes/deferrals immutable; no old six-role validator assumption |
| Stateful chart upgrades | Production CNPG CR/storage/WAL/synchronous/monitoring/plugin/ScheduledBackup/DSN; external ObjectStore/operator | Ordinary image upgrades preserve database identity/spec; major/operator/schema changes are distinct rehearsals; new render contract is not live PVC proof |
| Supported platforms | Compatibility Kubernetes 1.33–1.35, browser minimums/current+previous policy and declared matrix commands | Advertised support is a claim; W12 needs actual version/engine evidence. Current stable discovery cannot silently raise customer minimums |
| Release paths / exceptions | Release approval/upgrade/runtime-image schemas, automated validators, waiver/license policy | Inspect actual tag-specific scope; CVE acceptance does not grant license/accessibility/FIPS qualification or apply automatically to next tag |

Historical Flux ADR describes initial v1 fresh-only cutover from v0.3.x. Current generated compatibility contract explicitly supersedes its dated release-mode snapshot with fresh-and-upgrade 1.2.x; the retained rejection of automatic v0.3.x import/reset remains. W00 leaves the historical ADR intact and directs execution to current machine contracts.

Support evidence classifications: current source implemented; W00 static checks exercised; historical release/run evidence retained; live comparison topology observed; CNPG/Firefox/WebKit/cloud/scale/Rancher/human accessibility not newly qualified. None of those classes is interchangeable.
