// Friendly names for common operators. Unknown API groups retain their name.
const labels: Record<string, string> = {
  "cert-manager.io": "Cert Manager",
  "constraints.gatekeeper.sh": "Gatekeeper",
  "crd.projectcalico.org": "Calico",
  "gateway.networking.k8s.io": "Gateway API",
  "helm.toolkit.fluxcd.io": "Flux Helm",
  "kustomize.toolkit.fluxcd.io": "Flux Kustomize",
  "longhorn.io": "Longhorn",
  "monitoring.coreos.com": "Monitoring",
  "networking.istio.io": "Istio Networking",
  "notification.toolkit.fluxcd.io": "Flux Notifications",
  "security.istio.io": "Istio Security",
  "source.toolkit.fluxcd.io": "Flux Sources",
  "tekton.dev": "Tekton",
};

export function crdGroupLabel(group: string): string {
  return labels[group] ?? group;
}
