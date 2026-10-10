# Upgrade runner contract and migration boundary

This source inventory is bound to `6948446e` and all 71 tracked `scripts/**/*.sh` sources. `shell-inventory.json` records exact hashes, source line counts, categories and lexical signal locations. Signal presence does not establish ownership, cancellation, redaction, or runtime safety. The original plan's 65-script count remains historical.

The selected high-impact entrypoint is `scripts/upgrade-release.sh`. Its supported arguments are `[--yes] [--dry-run-only] <stable-v1-version>`, with `-h/--help`. Missing/duplicate/unknown/invalid versions exit 2; operational checks generally exit 1; an unhandled external command may preserve its own nonzero status. Default and `--dry-run-only` both perform backup and server-side dry run. They can create and drop a temporary restore database: they are not zero-write previews. `--yes` permits the Helm upgrade after those prerequisites.

Inputs include explicit Kubernetes context and exact HTTPS API identity, release/namespace, exact chart/release repository, private backup root, rollout timeout, local readiness port, readiness/capacity thresholds, optional pre-promotion signed artifacts, sanitized evidence directory, and explicit external backup/clean-schema confirmation. Optional RC flags identify a disposable decryption proof and request replacing the database from the verified backup. Secrets are retained only in the private backup artifacts; sanitized evidence hashes their checksum manifests rather than publishing values.

The established stage order is:

1. Parse exact v1 target and explicit Kubernetes context/API, dependencies and bounds.
2. Verify deployed Helm chart/version, newer target, backup permissions, ready schedulable nodes, fully observed/available deployments, PDB disruption allowance and backup space.
3. Verify exact release metadata, signatures, chart bytes/attestation, exact immutable first-party images, OCI chart equality, and target chart schema range.
4. Capture Helm values/manifests/history/recovery Secrets. Back up bundled PostgreSQL to a readable custom archive and verify a temporary restore's clean schema and optional encrypted proof; otherwise require explicit external backup/schema evidence. Preserve checksum inventory.
5. Server-side Helm dry run with preserved values and exact signed images; default and `--dry-run-only` stop with existing sanitized schema-v1 evidence.
6. For explicitly leased disposable RC only, quiesce server/worker and replace the live database. This currently has no UID lease validation in the shell and needs an actual fence before any DROP/rename.
7. Atomic Helm upgrade, exact image/readiness/deployment/schema observation, temporary owned port-forward and `/readyz` observation.
8. On later failure, attempt the prior Helm revision rollback; restore quiesced RC replicas when appropriate; terminate the owned readiness process and write failure evidence while preserving the original exit.

The typed draft in `internal/ops/upgrade` separates validation, backup, preview, mutation intent, observation, rollback assessment and cleanup. Every mutating stage requires a fresh namespace/release identity fence. RC replacement additionally requires an actual disposable namespace/pod/database lease. Checkpoints are private observation/intent records, separate from the unchanged public schema-v1 evidence. Resume initially stops for reconciliation/assessment; it does not replay an apply with uncertain outcome or assume cancellation rolled it back.

Runtime role selection must come from the byte-verified target chart's exact source references and signed target manifest. The historical shell's PostgreSQL 16/Valkey 8/Dex role lookups are a historical adapter, not current version authority. A new adapter must preserve historical targets while resolving qualified candidate roles without mutable tags, local `latest`, or an automatic PostgreSQL major change. Manifest schema and signer validation remain existing release-contract responsibilities; the draft lookup helper is not a signature verifier.

The shell is unchanged. The draft has no production adapter, bounded subprocess implementation, durable file-store implementation, CLI wiring, wrapper parity, failure-injection execution, or actual upgrade qualification yet. No live mutation has run. These are implementation steps, not completion claims.
