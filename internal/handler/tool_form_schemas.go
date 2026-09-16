package handler

// Curated per-tool configuration form schemas. These drive the Rancher-style
// "clean settings form" the UI renders when installing a tool: each field maps a
// human label to a real Helm values path for that chart, grouped into sections
// (Scaling / Resources / Networking / Storage / General). The form builds a
// values_override YAML from the field values; an "Edit YAML" tab is the escape
// hatch for anything not covered here.
//
// Only knobs that map to a real values path on the upstream chart are listed —
// we never invent fields a chart would reject. Tools without a curated schema
// fall back to the raw-YAML editor.

// ToolFormFieldType enumerates the input widgets the frontend renders.
const (
	toolFieldString  = "string"
	toolFieldNumber  = "number"
	toolFieldBoolean = "boolean"
	toolFieldSelect  = "select"
	toolFieldStorage = "storage" // size + storageClass pair (path is the size; StorageClassPath the class)
)

// ToolFormField is one row in the install form. Path is a dot-path into the
// chart values (e.g. "controller.resources.requests.cpu").
type ToolFormField struct {
	Path             string   `json:"path"`
	Label            string   `json:"label"`
	Type             string   `json:"type"`
	Group            string   `json:"group"`
	Default          string   `json:"default,omitempty"`
	Options          []string `json:"options,omitempty"`
	Help             string   `json:"help,omitempty"`
	Placeholder      string   `json:"placeholder,omitempty"`
	StorageClassPath string   `json:"storage_class_path,omitempty"` // for type=storage
}

// ToolFormSchema is the ordered set of fields for a tool's install form.
type ToolFormSchema struct {
	Fields []ToolFormField `json:"fields"`
}

func resourceFields(prefix, cpuReq, memReq, memLim string) []ToolFormField {
	return []ToolFormField{
		{Path: prefix + "requests.cpu", Label: "CPU request", Type: toolFieldString, Group: "Resources", Default: cpuReq, Placeholder: "100m", Help: "Guaranteed CPU. Kubernetes uses this for scheduling."},
		{Path: prefix + "requests.memory", Label: "Memory request", Type: toolFieldString, Group: "Resources", Default: memReq, Placeholder: "128Mi"},
		{Path: prefix + "limits.memory", Label: "Memory limit", Type: toolFieldString, Group: "Resources", Default: memLim, Placeholder: "256Mi", Help: "Pod is OOM-killed if it exceeds this."},
	}
}

// toolFormSchemas is keyed by tool slug. Absent slug → raw-YAML editor only.
var toolFormSchemas = map[string]ToolFormSchema{
	"istio": {Fields: append([]ToolFormField{
		{Path: "base.defaultRevision", Label: "Default control plane revision", Type: toolFieldString, Group: "Base CRDs", Default: "default", Help: "Base CRDs are installed before the control plane."},
		{Path: "istiod.autoscaleEnabled", Label: "Autoscale the control plane", Type: toolFieldBoolean, Group: "Control plane", Default: "true"},
		{Path: "istiod.autoscaleMin", Label: "Minimum replicas", Type: toolFieldNumber, Group: "Control plane", Default: "2"},
		{Path: "istiod.autoscaleMax", Label: "Maximum replicas", Type: toolFieldNumber, Group: "Control plane", Default: "5"},
		{Path: "istiod.replicaCount", Label: "Replicas when autoscaling is disabled", Type: toolFieldNumber, Group: "Control plane", Default: "2"},
		{Path: "istiod.sidecarInjectorWebhook.enableNamespacesByDefault", Label: "Inject sidecars in all namespaces", Type: toolFieldBoolean, Group: "Injection", Default: "false", Help: "Leave off and opt in individual namespaces after reviewing workload compatibility."},
	}, resourceFields("istiod.resources.", "500m", "2048Mi", "4096Mi")...)},
	"longhorn": {Fields: []ToolFormField{
		{Path: "persistence.defaultClass", Label: "Make Longhorn the default StorageClass", Type: toolFieldBoolean, Group: "Storage", Default: "false", Help: "Leave off to preserve the existing cluster default."},
		{Path: "persistence.defaultClassReplicaCount", Label: "Replicas per volume", Type: toolFieldNumber, Group: "Storage", Default: "3", Help: "Requires enough eligible nodes to place replicas."},
		{Path: "persistence.reclaimPolicy", Label: "Volume reclaim policy", Type: toolFieldSelect, Group: "Storage", Default: "Retain", Options: []string{"Retain", "Delete"}, Help: "Retain preserves volume data when a claim is deleted."},
		{Path: "csi.kubeletRootDir", Label: "Kubelet root directory", Type: toolFieldString, Group: "Storage", Placeholder: "/var/lib/kubelet", Help: "Set only when the distribution uses a non-standard kubelet directory."},
	}},
	"neuvector": {Fields: []ToolFormField{
		{Path: "controller.replicas", Label: "Controller replicas", Type: toolFieldNumber, Group: "Scaling", Default: "3"},
		{Path: "cve.scanner.replicas", Label: "Scanner replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1"},
		{Path: "manager.svc.type", Label: "Manager service type", Type: toolFieldSelect, Group: "Networking", Default: "ClusterIP", Options: []string{"ClusterIP", "NodePort", "LoadBalancer"}, Help: "Configure authentication before exposing the manager outside the cluster."},
	}},
	"kube-state-metrics": {Fields: append([]ToolFormField{
		{Path: "replicas", Label: "Replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1", Help: "kube-state-metrics is sharded; 1 is fine for most clusters."},
	}, resourceFields("resources.", "10m", "32Mi", "64Mi")...)},

	"prometheus-node-exporter": {Fields: append([]ToolFormField{
		{Path: "hostNetwork", Label: "Use host network", Type: toolFieldBoolean, Group: "General", Default: "true", Help: "Required to read node-level metrics. Leave on unless your CNI forbids it."},
	}, resourceFields("resources.", "10m", "24Mi", "64Mi")...)},

	"trivy-operator": {Fields: append([]ToolFormField{
		{Path: "trivy.ignoreUnfixed", Label: "Ignore unfixed CVEs", Type: toolFieldBoolean, Group: "General", Default: "true", Help: "Hide vulnerabilities that have no available fix."},
		{Path: "trivy.severity", Label: "Report severities", Type: toolFieldString, Group: "General", Default: "CRITICAL,HIGH,MEDIUM", Placeholder: "CRITICAL,HIGH", Help: "Comma-separated severities to record."},
		{Path: "operator.scanJobTimeout", Label: "Scan job timeout", Type: toolFieldString, Group: "General", Default: "5m", Placeholder: "5m"},
	}, resourceFields("trivy.resources.", "100m", "128Mi", "512Mi")...)},

	"fluent-bit": {Fields: resourceFields("resources.", "50m", "64Mi", "128Mi")},

	"ingress-nginx": {Fields: append([]ToolFormField{
		{Path: "controller.replicaCount", Label: "Replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1"},
		{Path: "controller.service.type", Label: "Service type", Type: toolFieldSelect, Group: "Networking", Default: "LoadBalancer", Options: []string{"LoadBalancer", "NodePort", "ClusterIP"}, Help: "How the ingress controller is exposed."},
		{Path: "controller.ingressClassResource.name", Label: "IngressClass name", Type: toolFieldString, Group: "Networking", Default: "nginx", Placeholder: "nginx"},
		{Path: "controller.metrics.enabled", Label: "Expose Prometheus metrics", Type: toolFieldBoolean, Group: "Networking", Default: "true"},
	}, resourceFields("controller.resources.", "100m", "128Mi", "512Mi")...)},

	"cert-manager": {Fields: append([]ToolFormField{
		{Path: "installCRDs", Label: "Install CRDs", Type: toolFieldBoolean, Group: "General", Default: "true", Help: "Required on first install unless the cert-manager CRDs are already present."},
		{Path: "replicaCount", Label: "Replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1"},
	}, resourceFields("resources.", "10m", "32Mi", "128Mi")...)},

	"gatekeeper": {Fields: append([]ToolFormField{
		{Path: "replicas", Label: "Controller replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1"},
		{Path: "auditInterval", Label: "Audit interval (seconds)", Type: toolFieldNumber, Group: "General", Default: "60", Help: "How often Gatekeeper re-evaluates existing resources against policy."},
	}, resourceFields("controllerManager.resources.", "100m", "256Mi", "512Mi")...)},
}

func toolFormSchemaFor(slug string) *ToolFormSchema {
	if s, ok := toolFormSchemas[slug]; ok {
		return &s
	}
	return nil
}
