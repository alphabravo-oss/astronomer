#!/usr/bin/env bash
# Bootstrap a local k3d cluster and deploy the Astronomer Go management plane.
#
# What this script does, in order:
#   1. Create the k3d cluster (with port mappings) if missing.
#   2. Install the Gateway API standard CRDs.
#   3. Install NGINX Gateway Fabric (provides the `nginx` GatewayClass).
#   4. Build the astronomer Docker images and import them into k3d.
#   5. helm install astronomer with the right host + server URL.
#
# Argo CD is NOT installed here — the astronomer-server self-management loop
# (internal/server/self_manage_argocd.go) installs Argo and registers the
# astronomer-self-manage Application automatically about 30s after the
# server pod is ready.
#
# Usage:
#   ./scripts/k3d-bootstrap.sh
#   CLUSTER=foo IMG_TAG=v0.2.0 HOST=astronomer.foo.nip.io ./scripts/k3d-bootstrap.sh
#
# Env vars:
#   CLUSTER       k3d cluster name                           (default: astronomer-mgmt)
#   IMG_TAG       Image tag to build / deploy                (default: dev)
#   IMG_REGISTRY  First-party image registry                 (default: ghcr.io/alphabravo-oss)
#   NAMESPACE     Astronomer release namespace               (default: astronomer)
#   HOST          External hostname for the dashboard        (default: astronomer.localtest.me)
#   HTTP_PORT     Host port mapped to the gateway :80        (default: 8080)
#   SERVER_URL    Override the externally-reachable URL      (default: http://${HOST}:${HTTP_PORT})
#   GW_API_VER    Gateway API release tag for the CRDs       (default: v1.4.1)
#   NGF_VERSION   NGINX Gateway Fabric chart version         (default: 2.6.0)
#   SKIP_BUILD    Skip docker build step                     (default: 0)
#   SKIP_PREREQS  Skip Gateway API + NGF install             (default: 0)

set -euo pipefail

CLUSTER="${CLUSTER:-astronomer-mgmt}"
IMG_TAG="${IMG_TAG:-dev}"
IMG_REGISTRY="${IMG_REGISTRY:-ghcr.io/alphabravo-oss}"
NAMESPACE="${NAMESPACE:-astronomer}"
HOST="${HOST:-astronomer.localtest.me}"
HTTP_PORT="${HTTP_PORT:-8080}"
SERVER_URL="${SERVER_URL:-http://${HOST}:${HTTP_PORT}}"
GW_API_VER="${GW_API_VER:-v1.4.1}"
NGF_VERSION="${NGF_VERSION:-2.6.0}"
SKIP_BUILD="${SKIP_BUILD:-0}"
SKIP_PREREQS="${SKIP_PREREQS:-0}"

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
require helm

IMG_SERVER="${IMG_REGISTRY}/astronomer-go-server:${IMG_TAG}"
IMG_AGENT="${IMG_REGISTRY}/astronomer-go-agent:${IMG_TAG}"
IMG_WORKER="${IMG_REGISTRY}/astronomer-go-worker:${IMG_TAG}"
IMG_MIGRATE="${IMG_REGISTRY}/astronomer-go-migrate:${IMG_TAG}"
# Frontend image name kept as astronomer-frontend to match deploy/chart/values.yaml.
IMG_FRONTEND="${IMG_REGISTRY}/astronomer-frontend:${IMG_TAG}"
IMG_SHELL="${IMG_REGISTRY}/astronomer-shell:${IMG_TAG}"

# ── 1. Create cluster (if missing) ───────────────────────────────────────────
step "Ensuring k3d cluster '${CLUSTER}' exists"
if k3d cluster list --no-headers 2>/dev/null | awk '{print $1}' | grep -qx "${CLUSTER}"; then
  info "cluster already exists"
else
  # Disable k3s's built-in Traefik. We install NGINX Gateway Fabric below;
  # both controllers fight for :80 via type=LoadBalancer Services and
  # whichever loses leaves the gateway routes returning 404 from outside
  # the cluster.
  k3d cluster create "${CLUSTER}" \
    --port "${HTTP_PORT}:80@loadbalancer" \
    --port "8443:443@loadbalancer" \
    -p "8000:30080@loadbalancer" \
    --k3s-arg "--disable=traefik@server:*" \
    --wait
fi

# ── 2. Gateway API CRDs + NGINX Gateway Fabric ───────────────────────────────
# The chart in deploy/chart deploys a Gateway + HTTPRoutes; a GatewayClass +
# controller need to exist before those reconcile cleanly.
if [[ "${SKIP_PREREQS}" != "1" ]]; then
  step "Installing Gateway API CRDs (${GW_API_VER})"
  kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GW_API_VER}/standard-install.yaml"

  step "Installing NGINX Gateway Fabric (chart ${NGF_VERSION})"
  helm upgrade --install ngf \
    oci://ghcr.io/nginx/charts/nginx-gateway-fabric \
    --version "${NGF_VERSION}" \
    --create-namespace --namespace nginx-gateway \
    --wait --timeout 5m

  step "Waiting for the 'nginx' GatewayClass to be Accepted"
  for _ in $(seq 1 30); do
    state=$(kubectl get gatewayclass nginx -o jsonpath='{.status.conditions[?(@.type=="Accepted")].status}' 2>/dev/null || true)
    if [[ "$state" == "True" ]]; then
      info "GatewayClass nginx is Accepted"
      break
    fi
    sleep 2
  done
else
  info "SKIP_PREREQS=1, skipping Gateway API CRDs + NGF install"
fi

# ── 3. Build images ──────────────────────────────────────────────────────────
if [[ "${SKIP_BUILD}" != "1" ]]; then
  step "Building Docker images (tag=${IMG_TAG})"
  make IMG_TAG="${IMG_TAG}" IMG_REGISTRY="${IMG_REGISTRY}" docker-build-all
else
  info "SKIP_BUILD=1, skipping docker build"
fi

# ── 4. Import images into k3d ────────────────────────────────────────────────
step "Importing images into k3d cluster"
k3d image import \
  "${IMG_SERVER}" "${IMG_AGENT}" "${IMG_WORKER}" "${IMG_MIGRATE}" "${IMG_FRONTEND}" "${IMG_SHELL}" \
  -c "${CLUSTER}"

# ── 5. Deploy astronomer ─────────────────────────────────────────────────────
step "Installing Helm chart into namespace '${NAMESPACE}'"
helm upgrade --install astronomer deploy/chart \
  --namespace "${NAMESPACE}" --create-namespace \
  -f deploy/chart/values.yaml \
  --set image.server.registry="${IMG_REGISTRY}" \
  --set image.worker.registry="${IMG_REGISTRY}" \
  --set image.agent.registry="${IMG_REGISTRY}" \
  --set image.migrate.registry="${IMG_REGISTRY}" \
  --set frontend.image.registry="${IMG_REGISTRY}" \
  --set preflight.image.registry="${IMG_REGISTRY}" \
  --set image.server.tag="${IMG_TAG}" \
  --set image.worker.tag="${IMG_TAG}" \
  --set image.agent.tag="${IMG_TAG}" \
  --set image.migrate.tag="${IMG_TAG}" \
  --set frontend.image.tag="${IMG_TAG}" \
  --set preflight.image.tag="${IMG_TAG}" \
  --set-string kubectlShell.image="${IMG_SHELL}" \
  --set config.serverURL="${SERVER_URL}" \
  --set config.corsAllowedOrigins="${SERVER_URL}" \
  --set ingress.enabled=false \
  --set "gateway.hosts={${HOST}}"

SERVER_DEPLOY="$(kubectl -n "${NAMESPACE}" get deploy -l app.kubernetes.io/component=server -o name | head -n1)"

# ── 6. Wait for server ───────────────────────────────────────────────────────
step "Waiting for server deployment to be Available"
kubectl -n "${NAMESPACE}" wait --for=condition=available --timeout=300s "${SERVER_DEPLOY}" \
  || warn "server did not become Available in 5 minutes; check 'kubectl -n ${NAMESPACE} get pods'"

# ── 7. Print access info ─────────────────────────────────────────────────────
BOOTSTRAP_PW=$(kubectl -n "${NAMESPACE}" get secret astronomer-bootstrap -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || true)

cat <<EOF

  Cluster:        ${CLUSTER}
  Namespace:      ${NAMESPACE}
  URL:            ${SERVER_URL}/
  Health:         ${SERVER_URL}/health/

  Bootstrap user: admin
  Bootstrap pw:   ${BOOTSTRAP_PW:-<see kubectl logs OR get secret>}

  Watch pods:     kubectl -n ${NAMESPACE} get pods -w
  Server logs:    kubectl -n ${NAMESPACE} logs -l app.kubernetes.io/component=server -f
  Argo self-mgr:  kubectl -n argocd get application astronomer-self-manage -w  # appears ~30s after server is Ready

EOF
