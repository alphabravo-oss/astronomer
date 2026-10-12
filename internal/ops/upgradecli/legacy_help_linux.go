//go:build linux

package upgradecli

// LegacyUpgradeHelp is the exact maintained old wrapper usage/environment text.
const LegacyUpgradeHelp = `Usage: scripts/upgrade-release.sh [--yes] [--dry-run-only] <version>

  version         Exact v1 stable release, for example v1.0.1 or 1.0.1
  --yes           Perform the upgrade after backup and server-side dry run
  --dry-run-only  Stop after backup and server-side Helm dry run

Environment:
  RELEASE             Helm release name (default: astronomer)
  NAMESPACE           Helm namespace (default: astronomer)
  CHART_REF           OCI chart reference
  RELEASE_REPO        GitHub release repository
  BACKUP_ROOT         Private backup parent (default: ./astronomer-upgrade-backups)
  TIMEOUT             Helm/rollout timeout (default: 15m)
  HEALTH_PORT         Temporary localhost readiness port (default: 18080)
  MIN_READY_NODES     Minimum schedulable Ready nodes (default: 1)
  MIN_BACKUP_FREE_KIB Free space required before backup (default: 1048576)
  KUBE_CONTEXT        Required explicit kubeconfig context
  EXPECTED_KUBE_API_SERVER
                      Required exact API server URL for KUBE_CONTEXT
  RELEASE_ARTIFACT_DIR
                      Optional pre-promotion artifact directory (no network download)
  SANITIZED_EVIDENCE_DIR
                      Sanitized report directory (default: ./astronomer-upgrade-evidence)
  EXTERNAL_DB_BACKUP_CONFIRMED=1
                      Required when bundled PostgreSQL is not present
  EXTERNAL_DB_SCHEMA_VERSION=<n>
                      Required for an external database; must be a clean schema
                      inside the target release's declared upgrade range. The
                      chart hook independently verifies the live database.
  RC_DECRYPT_PROOF_WEBHOOK_ID
                      Optional disposable webhook UUID whose encrypted row must
                      exist in the clean restored database
  RC_REPLACE_DATABASE_FROM_BACKUP=1
                      Owned-disposable RC only: replace the live database with
                      the verified backup restore before final target startup
`
