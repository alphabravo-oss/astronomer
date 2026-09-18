package handler

import (
	"errors"
)

const defaultManagementBackupImage = "ghcr.io/alphabravocompany/pgdump-s3:16-awscli"

var (
	errManagementBackupReconcileActive = errors.New("management backup reconciliation is active")
	errManagementBackupStaleGeneration = errors.New("management backup generation is stale")
)

const managementBackupDumpScript = `set -eu
STAMP="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
DOW="$(date -u +%u)"
DOM="$(date -u +%d)"
BUCKET="${MANAGEMENT_BACKUP_BUCKET}"
PREFIX="${MANAGEMENT_BACKUP_PREFIX:-astronomer-pg}"
RELEASE="${MANAGEMENT_BACKUP_RELEASE}"
DAILY_KEY="${PREFIX}/${RELEASE}/daily/${STAMP}.pgcustom"
WEEKLY_KEY="${PREFIX}/${RELEASE}/weekly/${STAMP}.pgcustom"
MONTHLY_KEY="${PREFIX}/${RELEASE}/monthly/${STAMP}.pgcustom"
DUMP_PATH="/tmp/${STAMP}.pgcustom"
export AWS_SHARED_CREDENTIALS_FILE="/var/run/aws/credentials"
export AWS_DEFAULT_REGION="${MANAGEMENT_BACKUP_REGION}"
AWS_S3_FLAGS=""
if [ -n "${MANAGEMENT_BACKUP_ENDPOINT:-}" ]; then
  AWS_S3_FLAGS="--endpoint-url ${MANAGEMENT_BACKUP_ENDPOINT}"
fi
echo "Starting pg_dump at ${STAMP} (release=${RELEASE} dest=${MANAGEMENT_BACKUP_DEST_NAME})"
pg_dump --dbname="${DATABASE_URL}" --format=custom --no-owner --no-acl --file="${DUMP_PATH}"
DUMP_SIZE="$(wc -c < "${DUMP_PATH}")"
echo "pg_dump complete: ${DUMP_SIZE} bytes"
echo "Uploading daily: s3://${BUCKET}/${DAILY_KEY}"
aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${DAILY_KEY}"
if [ "${DOW}" = "7" ]; then
  echo "Promoting to weekly: s3://${BUCKET}/${WEEKLY_KEY}"
  aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${WEEKLY_KEY}"
fi
if [ "${DOM}" = "01" ]; then
  echo "Promoting to monthly: s3://${BUCKET}/${MONTHLY_KEY}"
  aws s3 cp ${AWS_S3_FLAGS} "${DUMP_PATH}" "s3://${BUCKET}/${MONTHLY_KEY}"
fi
rm -f "${DUMP_PATH}"
prune_tier() {
  TIER="$1"
  KEEP="$2"
  PFX="${PREFIX}/${RELEASE}/${TIER}/"
  echo "Pruning ${TIER} retention (keep ${KEEP})"
  KEYS="$(aws s3api list-objects-v2 ${AWS_S3_FLAGS} --bucket "${BUCKET}" --prefix "${PFX}" --query 'Contents[].Key' --output text 2>/dev/null || echo '')"
  if [ -z "${KEYS}" ] || [ "${KEYS}" = "None" ]; then
    echo "  (no objects under ${PFX})"
    return 0
  fi
  echo "${KEYS}" | tr '\t' '\n' | tr ' ' '\n' | sed '/^$/d' | sort -r | tail -n +"$((KEEP + 1))" | while read -r OLD; do
    [ -z "${OLD}" ] && continue
    echo "  rm s3://${BUCKET}/${OLD}"
    aws s3 rm ${AWS_S3_FLAGS} "s3://${BUCKET}/${OLD}" || true
  done
}
prune_tier "daily"   "${MANAGEMENT_BACKUP_KEEP_DAILY}"
prune_tier "weekly"  "${MANAGEMENT_BACKUP_KEEP_WEEKLY}"
prune_tier "monthly" "${MANAGEMENT_BACKUP_KEEP_MONTHLY}"
echo "Backup OK."
`

// ManagementBackupDestinationWrite is the create/update body.
// openapi:request ManagementBackupDestinationWriteRequest
type ManagementBackupDestinationWrite struct {
	Name        string `json:"name" validate:"required,max=255"`
	Bucket      string `json:"bucket" validate:"required,max=255"`
	Prefix      string `json:"prefix"`
	Region      string `json:"region"`
	EndpointURL string `json:"endpoint_url"`
	AccessKey   string `json:"access_key"`
	SecretKey   string `json:"secret_key"`
	Schedule    string `json:"schedule"`
	Enabled     *bool  `json:"enabled"`
	KeepDaily   *int32 `json:"keep_daily"`
	KeepWeekly  *int32 `json:"keep_weekly"`
	KeepMonthly *int32 `json:"keep_monthly"`
}

// GetDestination returns the exact durable destination state referenced by
// deletion receipts. Credentials remain redacted by destinationView.
