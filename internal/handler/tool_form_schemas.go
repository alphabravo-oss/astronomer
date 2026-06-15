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
