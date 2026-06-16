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
