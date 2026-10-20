// Operator charts should start in stable system namespaces. Project
// namespaces may intentionally enforce deny-all egress, quotas, and Pod
// Security policies that prevent an operator from reaching the Kubernetes
// API. The field stays editable for clusters with an established convention.
const RECOMMENDED_NAMESPACES: Record<string, string> = {
  constellation: "astronomer-constellation",
  "kube-state-metrics": "astronomer-monitoring",
  "prometheus-node-exporter": "astronomer-monitoring",
  "metrics-server": "astronomer-metrics-server",
  "kube-prometheus-stack": "astronomer-kube-prometheus",
  grafana: "astronomer-grafana",
  loki: "astronomer-loki",
  "trivy-operator": "astronomer-trivy-system",
  "cert-manager": "astronomer-cert-manager",
  "ingress-nginx": "astronomer-ingress-nginx",
  "external-secrets": "astronomer-external-secrets",
  kyverno: "astronomer-kyverno",
  longhorn: "longhorn-system",
  gatekeeper: "astronomer-gatekeeper-system",
  "fluent-bit": "astronomer-logging",
  "external-dns": "astronomer-external-dns",
  velero: "velero",
  "opentelemetry-collector": "astronomer-opentelemetry",
  tempo: "astronomer-tempo",
  keda: "astronomer-keda",
  "cloudnative-pg": "cnpg-system",
};

export function recommendedCatalogNamespace(chartName: string): string {
  return RECOMMENDED_NAMESPACES[chartName.toLowerCase()] ?? "default";
}
