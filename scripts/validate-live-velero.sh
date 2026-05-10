#!/usr/bin/env bash
# Validate a real Velero backup + restore path through Astronomer:
#   install prereqs -> create BSL via Astronomer -> run backup -> verify S3 artifacts ->
#   restore into a different namespace -> verify restored object
#
# Usage:
#   ASTRO_USERNAME=admin ASTRO_PASSWORD=... ./scripts/validate-live-velero.sh
#   AUTH_TOKEN=... ./scripts/validate-live-velero.sh
#
# Optional env:
#   BASE_URL        default: http://astronomer.localtest.me:8080
#   REMOTE_CONTEXT  default: k3d-astronomer-remote
#   CLUSTER_ID      default: first active non-local cluster
#   EVENT_TIMEOUT   default: 120

set -euo pipefail

BASE_URL="${BASE_URL:-http://astronomer.localtest.me:8080}"
API_BASE="${BASE_URL%/}/api/v1"
REMOTE_CONTEXT="${REMOTE_CONTEXT:-k3d-astronomer-remote}"
CLUSTER_ID="${CLUSTER_ID:-}"
EVENT_TIMEOUT="${EVENT_TIMEOUT:-120}"
AUTH_TOKEN="${AUTH_TOKEN:-}"
ASTRO_USERNAME="${ASTRO_USERNAME:-}"
ASTRO_PASSWORD="${ASTRO_PASSWORD:-}"

RUN_ID="$(date +%s)"
SRC_NS="velero-src-${RUN_ID}"
DST_NS="velero-dst-${RUN_ID}"
STORAGE_NAME="velero-verify-${RUN_ID}"
PREFIX="verify-${RUN_ID}"
BACKUP_NAME="velero-backup-${RUN_ID}"
MINIO_NAMESPACE="minio"
MINIO_BUCKET="velero"
MINIO_USER="minioadmin"
MINIO_PASSWORD="minioadmin"
MINIO_ENDPOINT="http://minio.minio.svc.cluster.local:9000"
STORAGE_ID=""
RESTORE_ID=""

cleanup() {
  kubectl --context "${REMOTE_CONTEXT}" delete namespace "${SRC_NS}" "${DST_NS}" --ignore-not-found >/dev/null 2>&1 || true
  kubectl --context "${REMOTE_CONTEXT}" -n velero delete backup "${VELERO_BACKUP_NAME:-}" restore "${VELERO_RESTORE_NAME:-}" --ignore-not-found >/dev/null 2>&1 || true
  kubectl --context "${REMOTE_CONTEXT}" -n velero delete backupstoragelocation "${STORAGE_NAME}" --ignore-not-found >/dev/null 2>&1 || true
  kubectl --context "${REMOTE_CONTEXT}" -n velero delete secret "${STORAGE_NAME}-credentials" --ignore-not-found >/dev/null 2>&1 || true
  if [[ -n "${AUTH_TOKEN}" && -n "${STORAGE_ID}" ]]; then
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" -X DELETE "${API_BASE}/backups/storage/${STORAGE_ID}/" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

require curl
require jq
require kubectl
require helm

if [[ -z "${AUTH_TOKEN}" ]]; then
  if [[ -z "${ASTRO_USERNAME}" || -z "${ASTRO_PASSWORD}" ]]; then
    echo "set AUTH_TOKEN or ASTRO_USERNAME and ASTRO_PASSWORD" >&2
    exit 1
  fi
  AUTH_TOKEN="$(
    curl -fsS \
      -H 'Content-Type: application/json' \
      -X POST "${API_BASE}/auth/login/" \
      -d "{\"username\":\"${ASTRO_USERNAME}\",\"password\":\"${ASTRO_PASSWORD}\"}" |
      jq -r '.data.token'
  )"
fi

if [[ -z "${CLUSTER_ID}" ]]; then
  CLUSTER_ID="$(
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/clusters/" |
      jq -r '.data[] | select(.status == "active" and (.is_local | not)) | .id' |
      head -n1
  )"
fi
if [[ -z "${CLUSTER_ID}" ]]; then
  echo "no active non-local cluster found; set CLUSTER_ID explicitly" >&2
  exit 1
fi

VELERO_BACKUP_NAME="backup-${BACKUP_NAME}"
VELERO_RESTORE_NAME="restore-${VELERO_BACKUP_NAME}"

echo "Using cluster_id=${CLUSTER_ID} remote_context=${REMOTE_CONTEXT}"

kubectl --context "${REMOTE_CONTEXT}" apply -f - <<'YAML' >/dev/null
apiVersion: v1
kind: Namespace
metadata:
  name: minio
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: minio
  namespace: minio
spec:
  replicas: 1
  selector:
    matchLabels:
      app: minio
  template:
    metadata:
      labels:
        app: minio
    spec:
      containers:
      - name: minio
        image: quay.io/minio/minio:RELEASE.2025-04-22T22-12-26Z
        args: ["server", "/data", "--console-address", ":9001"]
        env:
        - name: MINIO_ROOT_USER
          value: minioadmin
        - name: MINIO_ROOT_PASSWORD
          value: minioadmin
        ports:
        - containerPort: 9000
          name: api
        - containerPort: 9001
          name: console
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: minio
  namespace: minio
spec:
  selector:
    app: minio
  ports:
  - name: api
    port: 9000
    targetPort: api
  - name: console
    port: 9001
    targetPort: console
YAML
kubectl --context "${REMOTE_CONTEXT}" -n "${MINIO_NAMESPACE}" rollout status deploy/minio --timeout=120s >/dev/null

kubectl --context "${REMOTE_CONTEXT}" -n "${MINIO_NAMESPACE}" run minio-mc \
  --image=minio/mc:RELEASE.2025-04-16T18-13-26Z \
  --restart=Never \
  --rm -i \
  --command -- \
  sh -c "mc alias set local ${MINIO_ENDPOINT} ${MINIO_USER} ${MINIO_PASSWORD} >/dev/null && mc mb -p local/${MINIO_BUCKET} >/dev/null || true" >/dev/null

helm repo add vmware-tanzu https://vmware-tanzu.github.io/helm-charts >/dev/null 2>&1 || true
helm repo update >/dev/null 2>&1
helm upgrade --install velero vmware-tanzu/velero \
  --kube-context "${REMOTE_CONTEXT}" \
  --namespace velero \
  --create-namespace \
  --set credentials.useSecret=false \
  --set backupsEnabled=false \
  --set snapshotsEnabled=false \
  --set deployNodeAgent=false \
  --set 'initContainers[0].name=velero-plugin-for-aws' \
  --set 'initContainers[0].image=velero/velero-plugin-for-aws:v1.13.1' \
  --set 'initContainers[0].imagePullPolicy=IfNotPresent' \
  --set 'initContainers[0].volumeMounts[0].mountPath=/target' \
  --set 'initContainers[0].volumeMounts[0].name=plugins' >/dev/null
kubectl --context "${REMOTE_CONTEXT}" -n velero rollout status deploy/velero --timeout=180s >/dev/null

kubectl --context "${REMOTE_CONTEXT}" create namespace "${SRC_NS}" >/dev/null
kubectl --context "${REMOTE_CONTEXT}" -n "${SRC_NS}" create configmap velero-verify \
  --from-literal=message='hello from backup verification' >/dev/null

CREATE_STORAGE_BODY="$(
  jq -n \
    --arg name "${STORAGE_NAME}" \
    --arg cluster "${CLUSTER_ID}" \
    --arg prefix "${PREFIX}" \
    --arg endpoint "${MINIO_ENDPOINT}" \
    --arg access "${MINIO_USER}" \
    --arg secret "${MINIO_PASSWORD}" \
    '{
      name: $name,
      storage_type: "minio",
      bucket: "velero",
      prefix: $prefix,
      region: "us-east-1",
      endpoint_url: $endpoint,
      access_key: $access,
      secret_key: $secret,
      is_default: true,
      cluster_id: $cluster,
      velero_namespace: "velero"
    }'
)"

STORAGE_ID="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X POST \
    "${API_BASE}/backups/storage/" \
    -d "${CREATE_STORAGE_BODY}" |
    jq -r '.data.id'
)"

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  bsl_phase="$(kubectl --context "${REMOTE_CONTEXT}" -n velero get backupstoragelocation "${STORAGE_NAME}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  echo "bsl-poll=${i} phase=[${bsl_phase}]"
  if [[ "${bsl_phase}" == "Available" ]]; then
    break
  fi
  sleep 1
done

CREATE_BACKUP_BODY="$(
  jq -n \
    --arg name "${BACKUP_NAME}" \
    --arg storage "${STORAGE_ID}" \
    --arg src "${SRC_NS}" \
    '{name:$name,storage_id:$storage,backup_type:"full",included_namespaces:[$src]}'
)"

BACKUP_ID="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X POST \
    "${API_BASE}/backups/" \
    -d "${CREATE_BACKUP_BODY}" |
    jq -r '.data.id'
)"

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  remote_phase="$(kubectl --context "${REMOTE_CONTEXT}" -n velero get backup "${VELERO_BACKUP_NAME}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  row_status="$(
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/backups/${BACKUP_ID}/" |
      jq -r '.data.status'
  )"
  echo "backup-poll=${i} remote=[${remote_phase}] row=[${row_status}]"
  if [[ "${remote_phase}" == "Completed" && "${row_status}" == "completed" ]]; then
    break
  fi
  sleep 2
done

curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/backups/${BACKUP_ID}/" |
  jq -e '.data.status == "completed"' >/dev/null

kubectl --context "${REMOTE_CONTEXT}" -n "${MINIO_NAMESPACE}" run minio-mc \
  --image=minio/mc:RELEASE.2025-04-16T18-13-26Z \
  --restart=Never \
  --rm -i \
  --command -- \
  sh -c "mc alias set local ${MINIO_ENDPOINT} ${MINIO_USER} ${MINIO_PASSWORD} >/dev/null && mc find local/${MINIO_BUCKET}/${PREFIX} --name '*${VELERO_BACKUP_NAME}.tar.gz'" >/dev/null

CREATE_RESTORE_BODY="$(
  jq -n \
    --arg src "${SRC_NS}" \
    --arg dst "${DST_NS}" \
    '{included_namespaces:[$src],namespace_mapping:{($src):$dst}}'
)"

RESTORE_ID="$(
  curl -fsS \
    -H "Authorization: Bearer ${AUTH_TOKEN}" \
    -H 'Content-Type: application/json' \
    -X POST \
    "${API_BASE}/backups/${BACKUP_ID}/restore/" \
    -d "${CREATE_RESTORE_BODY}" |
    jq -r '.data.id'
)"

for i in $(seq 1 "${EVENT_TIMEOUT}"); do
  remote_phase="$(kubectl --context "${REMOTE_CONTEXT}" -n velero get restore "${VELERO_RESTORE_NAME}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  row_status="$(
    curl -fsS -H "Authorization: Bearer ${AUTH_TOKEN}" "${API_BASE}/backups/restores/?limit=50" |
      jq -r --arg id "${RESTORE_ID}" '.data[] | select(.id == $id) | .status'
  )"
  echo "restore-poll=${i} remote=[${remote_phase}] row=[${row_status}]"
  if [[ "${remote_phase}" == "Completed" && "${row_status}" == "completed" ]]; then
    break
  fi
  sleep 2
done

kubectl --context "${REMOTE_CONTEXT}" -n "${DST_NS}" get configmap velero-verify -o json |
  jq -e '.data.message == "hello from backup verification"' >/dev/null

echo "Validated Velero backup -> object storage -> restore against a live remote cluster"
