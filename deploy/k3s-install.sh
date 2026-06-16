#!/usr/bin/env bash
#
# k3s-install.sh — reproducible single-node Astronomer management cluster.
#
# Stands up the full stack the way we want it, from scratch:
#   k3s (NO flannel, NO traefik)  ->  Calico CNI  ->  cert-manager
#   ->  ingress-nginx  ->  import images  ->  helm install astronomer
# ArgoCD is bundled in the astronomer chart (astro-argocd subchart), not here.
#
# Why Calico (not flannel): flannel + k3s kube-proxy on this host drifted such
# that new pods couldn't reach ClusterIPs (CoreDNS/postgres). Calico is the
# conservative, battle-tested replacement and fixes that class of failure.
#
# Usage:
#   sudo ./deploy/k3s-install.sh            # full build (skips k3s reinstall if present)
#   sudo NUKE=1 ./deploy/k3s-install.sh     # tear down k3s first, then rebuild
#
# Re-runnable: every component uses `helm upgrade --install` / `kubectl apply`,
# so running it again converges rather than duplicating.
set -euo pipefail

# ── Tunables ──────────────────────────────────────────────────────────────────
HOST="${HOST:-astronomer.dev.alphabravo.io}"
NODE_IP="${NODE_IP:-$(hostname -I | awk '{print $1}')}"
POD_CIDR="${POD_CIDR:-10.42.0.0/16}"          # k3s default; Calico pool must match
CALICO_VERSION="${CALICO_VERSION:-v3.29.1}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.20.2}"
INGRESS_NGINX_VERSION="${INGRESS_NGINX_VERSION:-4.15.1}"
ARGOCD_CHART_VERSION="${ARGOCD_CHART_VERSION:-9.5.21}"

# Astronomer image tags (locally-built, imported into k3s containerd).
IMG_SERVER="${IMG_SERVER:-dd-1481a04}"
IMG_WORKER="${IMG_WORKER:-dd-62e7d0b}"
IMG_FRONTEND="${IMG_FRONTEND:-dd-2de57b1}"
IMG_MIGRATE="${IMG_MIGRATE:-dd-4caf792}"
IMG_AGENT="${IMG_AGENT:-f03fcf5-k3sfix}"

REPO_DIR="${REPO_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
SAVE_DIR="${SAVE_DIR:-/root/astro-rebuild}"   # holds astronomer-tls.yaml + values
# Authoritative install values (bootstrap creds + same password, secrets, postgres
# password, image tags, tls.source=secret). Kept on disk, NEVER in this script.
VALUES="${VALUES:-$SAVE_DIR/values-current.yaml}"
KUBECONFIG_PATH="/etc/rancher/k3s/k3s.yaml"

log(){ printf '\n\033[1;36m== %s ==\033[0m\n' "$*"; }

# ── 0. Optional nuke ──────────────────────────────────────────────────────────
if [[ "${NUKE:-0}" == "1" && -x /usr/local/bin/k3s-uninstall.sh ]]; then
  log "Tearing down existing k3s"
  /usr/local/bin/k3s-uninstall.sh || true
fi

# ── 1. k3s: no flannel (Calico provides CNI), no traefik (we use ingress-nginx) ─
if ! command -v k3s >/dev/null 2>&1; then
  log "Installing k3s (flannel-backend=none, disable traefik)"
  curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="server \
    --disable traefik \
    --flannel-backend=none \
    --disable-network-policy \
    --cluster-cidr=${POD_CIDR} \
    --write-kubeconfig-mode 644" sh -
else
  log "k3s already present — skipping install (set NUKE=1 to rebuild)"
fi
export KUBECONFIG="$KUBECONFIG_PATH"

log "Waiting for k3s API"
until kubectl get --raw=/readyz >/dev/null 2>&1; do sleep 3; done

# ── 2. Calico CNI (node is NotReady until this lands) ─────────────────────────
log "Installing Calico ${CALICO_VERSION}"
kubectl apply --server-side -f "https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/tigera-operator.yaml"
# The operator registers the Installation CRD asynchronously; wait for it to be
# established before applying the Installation CR (otherwise: "no matches for kind").
kubectl wait --for condition=established --timeout=120s crd/installations.operator.tigera.io
# VXLAN encapsulation = works across NAT/cloud/overlay hosts (incl. tailscale) without IPIP.
cat <<EOF | kubectl apply -f -
apiVersion: operator.tigera.io/v1
kind: Installation
metadata:
  name: default
spec:
  calicoNetwork:
    bgp: Disabled
    # CRITICAL on hosts with Tailscale/WireGuard: pin the node IP to the
    # kubernetes InternalIP (real eth0), otherwise Calico's first-found
    # autodetect grabs the tailscale0 IP and pods can't reach the API server
    # or any ClusterIP. This is the root cause of the whole networking saga.
    nodeAddressAutodetectionV4:
      kubernetes: NodeInternalIP
    ipPools:
      # Single-node cluster: no cross-node overlay needed, so use direct routing
      # (encapsulation None). VXLAN here also broke pod->node-own-IP traffic.
      - cidr: ${POD_CIDR}
        encapsulation: None
        natOutgoing: Enabled
        nodeSelector: all()
EOF
log "Waiting for node Ready (Calico up)"
kubectl wait --for=condition=Ready node --all --timeout=300s

# ── 3. Prerequisites ──────────────────────────────────────────────────────────
log "cert-manager ${CERT_MANAGER_VERSION}"
helm repo add jetstack https://charts.jetstack.io >/dev/null 2>&1 || true
helm repo update jetstack >/dev/null
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager --create-namespace \
  --version "${CERT_MANAGER_VERSION}" --set crds.enabled=true --wait --timeout 5m

log "ingress-nginx ${INGRESS_NGINX_VERSION}"
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx >/dev/null 2>&1 || true
helm repo update ingress-nginx >/dev/null
helm upgrade --install ingress-nginx ingress-nginx/ingress-nginx \
  --namespace ingress-nginx --create-namespace --version "${INGRESS_NGINX_VERSION}" \
  --set controller.ingressClassResource.name=nginx \
  --set controller.ingressClass=nginx \
  --set controller.service.type=LoadBalancer --wait --timeout 5m

# NOTE: ArgoCD is NOT installed standalone here — it ships as the astro-argocd
# subchart of the astronomer chart (deploy/chart), so `helm install astronomer`
# below brings it up in the astronomer namespace.

# ── 4. Import locally-built Astronomer images into k3s containerd ──────────────
log "Importing Astronomer images into containerd"
import_img(){ docker save "$1" | k3s ctr images import - >/dev/null && echo "  imported $1"; }
import_img "astronomer-go-server:${IMG_SERVER}"
import_img "astronomer-go-worker:${IMG_WORKER}"
import_img "astronomer-frontend:${IMG_FRONTEND}"
import_img "astronomer-go-migrate:${IMG_MIGRATE}"
import_img "astronomer-go-agent:${IMG_AGENT}"

# ── 5. Astronomer (fresh helm install; migrate.enabled=true runs all migrations) ─
log "helm install astronomer (bundles astro-argocd subchart)"
helm dependency build "${REPO_DIR}/deploy/chart"   # vendor argo-cd subchart
helm upgrade --install astronomer "${REPO_DIR}/deploy/chart" \
  --namespace astronomer --create-namespace \
  ${VALUES:+-f "$VALUES"} \
  --set image.pullPolicy=IfNotPresent \
  --set image.server.tag="${IMG_SERVER}" \
  --set image.worker.tag="${IMG_WORKER}" \
  --set image.migrate.tag="${IMG_MIGRATE}" \
  --set image.agent.tag="${IMG_AGENT}" \
  --set frontend.image.tag="${IMG_FRONTEND}" \
  --set migrate.enabled=true \
  --set ingress.enabled=true --set ingress.className=nginx --set ingress.host="${HOST}" \
  --set tls.source=secret --set tls.secretName=astronomer-tls \
  --wait --timeout 10m

# ── 6. Restore the saved UI TLS cert ──────────────────────────────────────────
if [[ -f "$SAVE_DIR/astronomer-tls.yaml" ]]; then
  log "Restoring UI TLS secret"
  kubectl apply -n astronomer -f "$SAVE_DIR/astronomer-tls.yaml"
fi

log "Done. UI: https://${HOST}  (login: admin@alphabravo.io)"
kubectl get pods -n astronomer
