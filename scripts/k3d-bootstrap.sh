#!/usr/bin/env bash
# Bootstrap a local k3d cluster and deploy the Astronomer Go management plane.
#
# Usage:
#   ./scripts/k3d-bootstrap.sh
#   CLUSTER=foo IMG_TAG=v0.2.0 DEPLOY_MODE=helm ./scripts/k3d-bootstrap.sh
#
# Env vars:
#   CLUSTER       k3d cluster name           (default: astronomer-mgmt)
#   IMG_TAG       Image tag to deploy        (default: dev)
#   DEPLOY_MODE   "helm" or "kubectl"        (default: helm)
#   NAMESPACE     Target namespace           (default: astronomer)
#   SKIP_BUILD    Skip docker build step     (default: 0)

set -euo pipefail

CLUSTER="${CLUSTER:-astronomer-mgmt}"
IMG_TAG="${IMG_TAG:-dev}"
DEPLOY_MODE="${DEPLOY_MODE:-helm}"
NAMESPACE="${NAMESPACE:-astronomer}"
SKIP_BUILD="${SKIP_BUILD:-0}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

step()    { printf "\n\033[1;36m==> %s\033[0m\n" "$*"; }
info()    { printf "\033[0;90m    %s\033[0m\n" "$*"; }
warn()    { printf "\033[1;33m[!] %s\033[0m\n" "$*" >&2; }
fail()    { printf "\033[1;31m[x] %s\033[0m\n" "$*" >&2; exit 1; }
require() { command -v "$1" >/dev/null 2>&1 || fail "missing required tool: $1"; }

require k3d
require kubectl
require docker
[[ "$DEPLOY_MODE" == "helm" ]] && require helm

IMG_SERVER="astronomer-go-server:${IMG_TAG}"
IMG_AGENT="astronomer-go-agent:${IMG_TAG}"
IMG_WORKER="astronomer-go-worker:${IMG_TAG}"
IMG_MIGRATE="astronomer-go-migrate:${IMG_TAG}"
# Frontend image name kept as astronomer-frontend to match deploy/chart/values.yaml.
IMG_FRONTEND="astronomer-frontend:${IMG_TAG}"

# ── 1. Create cluster (if missing) ───────────────────────────────────────────
step "Ensuring k3d cluster '${CLUSTER}' exists"
if k3d cluster list --no-headers 2>/dev/null | awk '{print $1}' | grep -qx "${CLUSTER}"; then
  info "cluster already exists"
else
  k3d cluster create "${CLUSTER}" \
    --port "8080:80@loadbalancer" \
    --port "8443:443@loadbalancer" \
    -p "8000:30080@loadbalancer" \
    --wait
fi

# ── 2. Build images ──────────────────────────────────────────────────────────
if [[ "${SKIP_BUILD}" != "1" ]]; then
  step "Building Docker images (tag=${IMG_TAG})"
  make IMG_TAG="${IMG_TAG}" docker-build-all
else
  info "SKIP_BUILD=1, skipping docker build"
fi

# ── 3. Import images into k3d ────────────────────────────────────────────────
step "Importing images into k3d cluster"
k3d image import \
  "${IMG_SERVER}" "${IMG_AGENT}" "${IMG_WORKER}" "${IMG_MIGRATE}" "${IMG_FRONTEND}" \
  -c "${CLUSTER}"

# ── 4. Deploy ────────────────────────────────────────────────────────────────
case "${DEPLOY_MODE}" in
  helm)
    step "Installing Helm chart into namespace '${NAMESPACE}'"
    helm upgrade --install astronomer deploy/chart \
      --namespace "${NAMESPACE}" --create-namespace \
      -f deploy/chart/values.yaml \
      --set image.server.tag="${IMG_TAG}" \
      --set image.worker.tag="${IMG_TAG}" \
      --set image.agent.tag="${IMG_TAG}" \
      --set image.migrate.tag="${IMG_TAG}" \
      --set frontend.image.tag="${IMG_TAG}"
    SERVER_DEPLOY="$(kubectl -n "${NAMESPACE}" get deploy -l app.kubernetes.io/component=server -o name | head -n1)"
    ;;
  kubectl)
    step "Applying raw manifests from deploy/k8s/"
    kubectl apply -f deploy/k8s/
    SERVER_DEPLOY="deployment/astronomer-server"
    ;;
  *)
    fail "unknown DEPLOY_MODE=${DEPLOY_MODE} (expected 'helm' or 'kubectl')"
    ;;
esac

# ── 5. Wait for server ───────────────────────────────────────────────────────
step "Waiting for server deployment to be Available"
kubectl -n "${NAMESPACE}" wait --for=condition=available --timeout=300s "${SERVER_DEPLOY}" \
  || warn "server did not become Available in 5 minutes; check 'kubectl -n ${NAMESPACE} get pods'"

# ── 6. Print access info ─────────────────────────────────────────────────────
step "Cluster ready"
INGRESS_HOST="astronomer.localtest.me"
if [[ "${DEPLOY_MODE}" == "helm" ]]; then
  HOST_FROM_ING=$(kubectl -n "${NAMESPACE}" get ingress -o jsonpath='{.items[0].spec.rules[0].host}' 2>/dev/null || true)
  [[ -n "$HOST_FROM_ING" ]] && INGRESS_HOST="$HOST_FROM_ING"
fi

cat <<EOF

  Cluster:      ${CLUSTER}
  Namespace:    ${NAMESPACE}
  Ingress URL:  http://${INGRESS_HOST}:8080/
  Health:       http://${INGRESS_HOST}:8080/health/

  Watch pods:   kubectl -n ${NAMESPACE} get pods -w
  Server logs:  kubectl -n ${NAMESPACE} logs -l app.kubernetes.io/component=server -f

EOF
