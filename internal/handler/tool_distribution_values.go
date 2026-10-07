package handler

import "strings"

// Distribution-aware install overrides.
//
// Tool charts often assume a "vanilla" node layout that breaks on specific
// Kubernetes distributions: k3s/k3d/RKE2 nodes have no /etc/machine-id file and
// keep container logs under /var/log/pods (CRI), OpenShift forbids hostPath +
// arbitrary UIDs without elevated SCC, and so on. Rather than make every
// operator hand-tune values per cluster, the install path detects the target
// cluster's distribution and merges the right overrides automatically.

// distributionFamily collapses the many distribution strings the platform may
// record into a small set of families that share install quirks.
func distributionFamily(distribution string) string {
	d := strings.ToLower(strings.TrimSpace(distribution))
	switch {
	case d == "":
		return ""
	case strings.Contains(d, "k3s"), strings.Contains(d, "k3d"):
		return "k3s"
	case strings.Contains(d, "rke2"):
		return "rke2"
	case strings.Contains(d, "openshift"), strings.Contains(d, "okd"):
		return "openshift"
	case strings.Contains(d, "eks"):
		return "eks"
	case strings.Contains(d, "aks"):
		return "aks"
	case strings.Contains(d, "gke"):
		return "gke"
	default:
		return "vanilla"
	}
}

// distributionInstallValues returns a YAML values snippet to merge UNDER the
// preset/user values for a given tool on a given distribution, or "" when no
// adaptation is needed. Because it is concatenated before the preset and user
// overrides, an explicit operator value still wins.
func distributionInstallValues(slug, distribution string) string {
	fam := distributionFamily(distribution)
	if fam == "" || fam == "vanilla" {
		return ""
	}
	if byFam, ok := distributionToolOverrides[slug]; ok {
		return byFam[fam]
	}
	return ""
}

// catalogInstallValues layers Astronomer's platform integration defaults under
// distribution-specific adaptations and operator input. The final layer stays
// operator-controlled, matching Helm's normal values precedence.
func catalogInstallValues(slug, distribution, operatorValues string) string {
	return mergeValueLayers(platformCatalogInstallValues(slug), distributionInstallValues(slug, distribution), operatorValues)
}

func platformCatalogInstallValues(slug string) string {
	if slug != "constellation" {
		return ""
	}
	return constellationSystemNamespaceExemptions
}

// Constellation's default admission policies intentionally reject privileged,
// host-networked, and host-PID workloads. Astronomer's supported system tools
// need some of those capabilities. Rancher solves the same interaction with a
// maintained feature-application namespace exemption list (including CIS,
// Longhorn, NeuVector, monitoring, Gatekeeper, Istio, and logging). Keep the
// exemption at the webhook namespace selector so tenant namespaces still pass
// through every Constellation policy and operators can replace this list in
// their explicit values override.
const constellationSystemNamespaceExemptions = `# astronomer: trusted namespaces used by supported platform applications and tools
admission:
  webhook:
    namespaceSelector:
      matchExpressions:
        - key: kubernetes.io/metadata.name
          operator: NotIn
          values:
            - astronomer-cert-manager
            - astronomer-external-dns
            - astronomer-external-secrets
            - astronomer-fluent-bit
            - astronomer-gatekeeper-system
            - astronomer-grafana
            - astronomer-ingress-nginx
            - astronomer-keda
            - astronomer-kube-prometheus
            - astronomer-kyverno
            - astronomer-logging
            - astronomer-loki
            - astronomer-metrics-server
            - astronomer-monitoring
            - astronomer-opentelemetry
            - astronomer-tempo
            - astronomer-trivy-system
            - cattle-neuvector-system
            - cert-manager
            - cis-operator-system
            - cnpg-system
            - ingress-nginx
            - istio-system
            - longhorn-system
            - velero
`

// fluentBitCRINodeVolumes drops the chart's default /etc/machine-id hostPath
// (absent on k3s/k3d/RKE2 containerized nodes) and points the tail input at the
// CRI pod-log directory.
const fluentBitCRINodeVolumes = `# astronomer: distribution override (CRI nodes lack a host machine identity file)
daemonSetVolumes:
  - name: varlog
    hostPath:
      path: /var/log
  - name: varlibcontainers
    hostPath:
      path: /var/log/pods
daemonSetVolumeMounts:
  - name: varlog
    mountPath: /var/log
  - name: varlibcontainers
    mountPath: /var/log/pods
    readOnly: true
`

// fluentBitOpenShift grants the privilege OpenShift requires for a node-level
// log collector reading hostPath container logs.
const fluentBitOpenShift = `# astronomer: distribution override (OpenShift SCC)
securityContext:
  privileged: true
  runAsUser: 0
podSecurityContext:
  runAsNonRoot: false
`

// distributionToolOverrides maps tool slug -> distribution family -> values YAML.
var distributionToolOverrides = map[string]map[string]string{
	"fluent-bit": {
		"k3s":       fluentBitCRINodeVolumes,
		"k3d":       fluentBitCRINodeVolumes,
		"rke2":      fluentBitCRINodeVolumes,
		"openshift": fluentBitOpenShift,
	},
	"prometheus-node-exporter": {
		// node-exporter mounts host paths; OpenShift needs the privileged SCC.
		"openshift": "# astronomer: distribution override (OpenShift SCC)\nsecurityContext:\n  privileged: true\n  runAsUser: 0\n",
	},
}
