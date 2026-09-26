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
	toolFieldString    = "string"
	toolFieldNumber    = "number"
	toolFieldBoolean   = "boolean"
	toolFieldSelect    = "select"
	toolFieldMultiline = "multiline"
	toolFieldStorage   = "storage" // size + storageClass pair (path is the size; StorageClassPath the class)
)

// ToolFormField is one row in the install form. Path is a dot-path into the
// chart values (e.g. "controller.resources.requests.cpu").
type ToolFormField struct {
	Path             string             `json:"path"`
	Label            string             `json:"label"`
	Type             string             `json:"type"`
	Group            string             `json:"group"`
	Default          string             `json:"default,omitempty"`
	Options          []string           `json:"options,omitempty"`
	Help             string             `json:"help,omitempty"`
	Placeholder      string             `json:"placeholder,omitempty"`
	StorageClassPath string             `json:"storage_class_path,omitempty"` // for type=storage
	Minimum          *float64           `json:"minimum,omitempty"`
	Maximum          *float64           `json:"maximum,omitempty"`
	Step             *float64           `json:"step,omitempty"`
	ShowWhen         *ToolFormCondition `json:"show_when,omitempty"`
}

// ToolFormCondition keeps conditional questions deliberately simple and
// deterministic. It covers the common Helm relationship (show replicas only
// when autoscaling is on/off) without evaluating chart-provided expressions in
// the browser or trusting the UI as a validation boundary.
type ToolFormCondition struct {
	Path   string `json:"path"`
	Equals string `json:"equals"`
}

func numberBound(value float64) *float64 { return &value }

func shownWhen(path, equals string) *ToolFormCondition {
	return &ToolFormCondition{Path: path, Equals: equals}
}

// ToolFormSchema is the ordered set of fields for a tool's install form.
type ToolFormSchema struct {
	Fields []ToolFormField `json:"fields"`
}

func resourceFields(prefix, cpuReq, memReq, memLim string) []ToolFormField {
	return []ToolFormField{
		{Path: prefix + "requests.cpu", Label: "CPU request", Type: toolFieldString, Group: "Resources", Placeholder: cpuReq, Help: "Guaranteed CPU. Kubernetes uses this for scheduling. Blank keeps the chart default."},
		{Path: prefix + "requests.memory", Label: "Memory request", Type: toolFieldString, Group: "Resources", Placeholder: memReq, Help: "Blank keeps the chart default."},
		{Path: prefix + "limits.memory", Label: "Memory limit", Type: toolFieldString, Group: "Resources", Placeholder: memLim, Help: "Pod is OOM-killed if it exceeds this. Blank keeps the chart default."},
	}
}

// toolFormSchemas is keyed by tool slug. Absent slug → raw-YAML editor only.
var toolFormSchemas = map[string]ToolFormSchema{
	"cis-operator": {Fields: append([]ToolFormField{
		{Path: "alerts.enabled", Label: "Create Prometheus alerts", Type: toolFieldBoolean, Group: "Monitoring", Default: "false", Help: "Enable CIS scan alert rules when a compatible Prometheus stack is present."},
		{Path: "alerts.severity", Label: "Alert severity", Type: toolFieldSelect, Group: "Monitoring", Default: "warning", Options: []string{"info", "warning", "critical"}},
		{Path: "securityScanJob.overrideTolerations", Label: "Override scan job tolerations", Type: toolFieldBoolean, Group: "Scheduling", Default: "false", Help: "Use only when scan jobs need a different toleration policy from the operator."},
	}, resourceFields("resources.", "100m", "128Mi", "256Mi")...)},
	"istio": {Fields: append([]ToolFormField{
		{Path: "base.defaultRevision", Label: "Default control plane revision", Type: toolFieldString, Group: "Base CRDs", Default: "default", Help: "Base CRDs are installed before the control plane."},
		{Path: "istiod.autoscaleEnabled", Label: "Autoscale the control plane", Type: toolFieldBoolean, Group: "Control plane", Default: "true"},
		{Path: "istiod.autoscaleMin", Label: "Minimum replicas", Type: toolFieldNumber, Group: "Control plane", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1), ShowWhen: shownWhen("istiod.autoscaleEnabled", "true")},
		{Path: "istiod.autoscaleMax", Label: "Maximum replicas", Type: toolFieldNumber, Group: "Control plane", Default: "5", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1), ShowWhen: shownWhen("istiod.autoscaleEnabled", "true")},
		{Path: "istiod.replicaCount", Label: "Replicas when autoscaling is disabled", Type: toolFieldNumber, Group: "Control plane", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1), ShowWhen: shownWhen("istiod.autoscaleEnabled", "false")},
		{Path: "istiod.sidecarInjectorWebhook.enableNamespacesByDefault", Label: "Inject sidecars in all namespaces", Type: toolFieldBoolean, Group: "Injection", Default: "false", Help: "Leave off and opt in individual namespaces after reviewing workload compatibility."},
		{Path: "istiod.pilot.traceSampling", Label: "Trace sampling (%)", Type: toolFieldNumber, Group: "Telemetry", Default: "1", Minimum: numberBound(0), Maximum: numberBound(100), Step: numberBound(0.1), Help: "Percentage of requests sampled by the control plane. Higher values increase telemetry volume."},
	}, resourceFields("istiod.resources.", "500m", "2048Mi", "4096Mi")...)},
	"longhorn": {Fields: []ToolFormField{
		{Path: "persistence.defaultClass", Label: "Make Longhorn the default StorageClass", Type: toolFieldBoolean, Group: "Storage", Default: "true", Help: "Turn off to preserve the existing cluster default."},
		{Path: "persistence.defaultClassReplicaCount", Label: "Replicas per volume", Type: toolFieldNumber, Group: "Storage", Default: "3", Minimum: numberBound(1), Maximum: numberBound(20), Step: numberBound(1), Help: "Requires enough eligible nodes to place replicas."},
		{Path: "persistence.reclaimPolicy", Label: "Volume reclaim policy", Type: toolFieldSelect, Group: "Storage", Default: "Delete", Options: []string{"Retain", "Delete"}, Help: "Retain preserves volume data when a claim is deleted."},
		{Path: "csi.kubeletRootDir", Label: "Kubelet root directory", Type: toolFieldString, Group: "Storage", Placeholder: "/var/lib/kubelet", Help: "Set only when the distribution uses a non-standard kubelet directory."},
		{Path: "defaultSettings.defaultDataPath", Label: "Default data path", Type: toolFieldString, Group: "Storage", Placeholder: "/var/lib/longhorn", Help: "Directory used for Longhorn replica data on eligible nodes."},
		{Path: "longhornUI.replicas", Label: "UI replicas", Type: toolFieldNumber, Group: "Scaling", Default: "2", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1)},
		{Path: "ingress.enabled", Label: "Expose the Longhorn UI through Ingress", Type: toolFieldBoolean, Group: "Networking", Default: "false", Help: "Configure authentication before exposing the Longhorn UI."},
		{Path: "ingress.host", Label: "Ingress host", Type: toolFieldString, Group: "Networking", Default: "sslip.io", Placeholder: "longhorn.example.com", ShowWhen: shownWhen("ingress.enabled", "true")},
		{Path: "metrics.serviceMonitor.enabled", Label: "Create a ServiceMonitor", Type: toolFieldBoolean, Group: "Monitoring", Default: "false"},
	}},
	"neuvector": {Fields: []ToolFormField{
		{Path: "controller.replicas", Label: "Controller replicas", Type: toolFieldNumber, Group: "Scaling", Default: "3", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1)},
		{Path: "cve.scanner.replicas", Label: "Scanner replicas", Type: toolFieldNumber, Group: "Scaling", Default: "3", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1)},
		{Path: "manager.svc.type", Label: "Manager service type", Type: toolFieldSelect, Group: "Networking", Default: "ClusterIP", Options: []string{"ClusterIP", "NodePort", "LoadBalancer"}, Help: "Configure authentication before exposing the manager outside the cluster."},
		{Path: "manager.ingress.enabled", Label: "Expose the manager through Ingress", Type: toolFieldBoolean, Group: "Networking", Default: "false"},
		{Path: "manager.ingress.host", Label: "Manager ingress host", Type: toolFieldString, Group: "Networking", Placeholder: "neuvector.example.com", ShowWhen: shownWhen("manager.ingress.enabled", "true")},
		{Path: "enforcer.enabled", Label: "Enable runtime enforcers", Type: toolFieldBoolean, Group: "Runtime", Default: "true", Help: "Deploys a privileged DaemonSet on Linux nodes."},
		{Path: "leastPrivilege", Label: "Use least-privilege service accounts", Type: toolFieldBoolean, Group: "Security", Default: "false"},
		{Path: "internal.autoGenerateCert", Label: "Generate internal certificates", Type: toolFieldBoolean, Group: "Security", Default: "true"},
	}},
	"kube-state-metrics": {Fields: append([]ToolFormField{
		{Path: "replicas", Label: "Replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1), Help: "kube-state-metrics is sharded; 1 is fine for most clusters."},
		{Path: "autosharding.enabled", Label: "Automatically shard collectors", Type: toolFieldBoolean, Group: "Scaling", Default: "false"},
		{Path: "prometheus.monitor.enabled", Label: "Create a ServiceMonitor", Type: toolFieldBoolean, Group: "Monitoring", Default: "false"},
		{Path: "selfMonitor.enabled", Label: "Expose self-metrics", Type: toolFieldBoolean, Group: "Monitoring", Default: "false"},
		{Path: "hostNetwork", Label: "Use host network", Type: toolFieldBoolean, Group: "Networking", Default: "false"},
	}, resourceFields("resources.", "10m", "32Mi", "64Mi")...)},

	"prometheus-node-exporter": {Fields: append([]ToolFormField{
		{Path: "hostNetwork", Label: "Use host network", Type: toolFieldBoolean, Group: "General", Default: "true", Help: "Required to read node-level metrics. Leave on unless your CNI forbids it."},
		{Path: "hostPID", Label: "Use host PID namespace", Type: toolFieldBoolean, Group: "General", Default: "true"},
		{Path: "hostRootFsMount.enabled", Label: "Mount the host root filesystem", Type: toolFieldBoolean, Group: "General", Default: "true"},
		{Path: "service.port", Label: "Metrics port", Type: toolFieldNumber, Group: "Networking", Default: "9100", Minimum: numberBound(1), Maximum: numberBound(65535), Step: numberBound(1), Help: "When host networking is enabled, this port must be free on every node."},
		{Path: "prometheus.monitor.enabled", Label: "Create a ServiceMonitor", Type: toolFieldBoolean, Group: "Monitoring", Default: "false"},
	}, resourceFields("resources.", "10m", "24Mi", "64Mi")...)},

	"trivy-operator": {Fields: append([]ToolFormField{
		{Path: "trivy.ignoreUnfixed", Label: "Ignore unfixed CVEs", Type: toolFieldBoolean, Group: "General", Default: "false", Help: "Hide vulnerabilities that have no available fix."},
		{Path: "trivy.severity", Label: "Report severities", Type: toolFieldString, Group: "General", Default: "UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL", Placeholder: "CRITICAL,HIGH", Help: "Comma-separated severities to record."},
		{Path: "operator.scanJobTimeout", Label: "Scan job timeout", Type: toolFieldString, Group: "General", Default: "5m", Placeholder: "5m"},
		{Path: "operator.scanJobsConcurrentLimit", Label: "Concurrent scan jobs", Type: toolFieldNumber, Group: "Scaling", Default: "10", Minimum: numberBound(1), Maximum: numberBound(1000), Step: numberBound(1)},
		{Path: "operator.vulnerabilityScannerEnabled", Label: "Scan workload vulnerabilities", Type: toolFieldBoolean, Group: "Scanners", Default: "true"},
		{Path: "operator.configAuditScannerEnabled", Label: "Scan Kubernetes configuration", Type: toolFieldBoolean, Group: "Scanners", Default: "true"},
		{Path: "serviceMonitor.enabled", Label: "Create a ServiceMonitor", Type: toolFieldBoolean, Group: "Monitoring", Default: "false"},
	}, resourceFields("trivy.resources.", "100m", "128Mi", "512Mi")...)},

	"fluent-bit": {Fields: append([]ToolFormField{
		{Path: "kind", Label: "Workload type", Type: toolFieldSelect, Group: "General", Default: "DaemonSet", Options: []string{"DaemonSet", "Deployment"}, Help: "DaemonSet collects logs from every node. Deployment is for centralized network inputs."},
		{Path: "replicaCount", Label: "Deployment replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1), ShowWhen: shownWhen("kind", "Deployment")},
		{Path: "logLevel", Label: "Fluent Bit log level", Type: toolFieldSelect, Group: "General", Default: "info", Options: []string{"off", "error", "warn", "info", "debug", "trace"}},
		{Path: "flush", Label: "Flush interval (seconds)", Type: toolFieldNumber, Group: "General", Default: "1", Minimum: numberBound(1), Maximum: numberBound(60), Step: numberBound(1)},
		{Path: "hotReload.enabled", Label: "Hot reload configuration", Type: toolFieldBoolean, Group: "General", Default: "true"},
		{Path: "serviceMonitor.enabled", Label: "Create a ServiceMonitor", Type: toolFieldBoolean, Group: "Monitoring", Default: "false"},
		{Path: "config.outputs", Label: "Output configuration", Type: toolFieldMultiline, Group: "Pipeline", Help: "Define at least one reachable Fluent Bit output. Use the Logging workflow for Astronomer-managed destinations."},
	}, resourceFields("resources.", "50m", "64Mi", "128Mi")...)},

	"ingress-nginx": {Fields: append([]ToolFormField{
		{Path: "controller.autoscaling.enabled", Label: "Autoscale the controller", Type: toolFieldBoolean, Group: "Scaling", Default: "false"},
		{Path: "controller.replicaCount", Label: "Replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1), ShowWhen: shownWhen("controller.autoscaling.enabled", "false")},
		{Path: "controller.autoscaling.minReplicas", Label: "Minimum replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1), ShowWhen: shownWhen("controller.autoscaling.enabled", "true")},
		{Path: "controller.autoscaling.maxReplicas", Label: "Maximum replicas", Type: toolFieldNumber, Group: "Scaling", Default: "11", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1), ShowWhen: shownWhen("controller.autoscaling.enabled", "true")},
		{Path: "controller.service.type", Label: "Service type", Type: toolFieldSelect, Group: "Networking", Default: "LoadBalancer", Options: []string{"LoadBalancer", "NodePort", "ClusterIP"}, Help: "How the ingress controller is exposed."},
		{Path: "controller.service.externalTrafficPolicy", Label: "External traffic policy", Type: toolFieldSelect, Group: "Networking", Default: "", Options: []string{"", "Cluster", "Local"}},
		{Path: "controller.ingressClassResource.name", Label: "IngressClass name", Type: toolFieldString, Group: "Networking", Default: "nginx", Placeholder: "nginx"},
		{Path: "controller.hostPort.enabled", Label: "Bind host ports", Type: toolFieldBoolean, Group: "Networking", Default: "false", Help: "Requires ports 80 and 443 to be free on eligible nodes."},
		{Path: "controller.metrics.enabled", Label: "Expose Prometheus metrics", Type: toolFieldBoolean, Group: "Networking", Default: "true"},
		{Path: "controller.metrics.serviceMonitor.enabled", Label: "Create a ServiceMonitor", Type: toolFieldBoolean, Group: "Monitoring", Default: "false", ShowWhen: shownWhen("controller.metrics.enabled", "true")},
		{Path: "controller.admissionWebhooks.enabled", Label: "Enable admission webhooks", Type: toolFieldBoolean, Group: "General", Default: "true"},
	}, resourceFields("controller.resources.", "100m", "128Mi", "512Mi")...)},

	"cert-manager": {Fields: append([]ToolFormField{
		{Path: "crds.enabled", Label: "Install CRDs", Type: toolFieldBoolean, Group: "General", Default: "true", Help: "Required on first install unless the cert-manager CRDs are already present."},
		{Path: "replicaCount", Label: "Replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1)},
		{Path: "webhook.replicaCount", Label: "Webhook replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1)},
		{Path: "cainjector.replicaCount", Label: "CA injector replicas", Type: toolFieldNumber, Group: "Scaling", Default: "1", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1)},
		{Path: "prometheus.enabled", Label: "Expose Prometheus metrics", Type: toolFieldBoolean, Group: "Monitoring", Default: "true"},
		{Path: "startupapicheck.enabled", Label: "Wait for the cert-manager API", Type: toolFieldBoolean, Group: "General", Default: "true", Help: "Keeps installation in progress until the webhook CA is injected and the cert-manager API answers successfully."},
	}, resourceFields("resources.", "10m", "32Mi", "128Mi")...)},

	"gatekeeper": {Fields: append([]ToolFormField{
		{Path: "replicas", Label: "Controller replicas", Type: toolFieldNumber, Group: "Scaling", Default: "3", Minimum: numberBound(1), Maximum: numberBound(100), Step: numberBound(1)},
		{Path: "auditInterval", Label: "Audit interval (seconds)", Type: toolFieldNumber, Group: "General", Default: "60", Minimum: numberBound(1), Maximum: numberBound(86400), Step: numberBound(1), Help: "How often Gatekeeper re-evaluates existing resources against policy."},
		{Path: "constraintViolationsLimit", Label: "Violations retained per constraint", Type: toolFieldNumber, Group: "General", Default: "20", Minimum: numberBound(1), Maximum: numberBound(10000), Step: numberBound(1)},
		{Path: "enableExternalData", Label: "Enable external data providers", Type: toolFieldBoolean, Group: "General", Default: "true"},
	}, resourceFields("controllerManager.resources.", "100m", "256Mi", "512Mi")...)},
}

func toolFormSchemaFor(slug string) *ToolFormSchema {
	if s, ok := toolFormSchemas[slug]; ok {
		return &s
	}
	return nil
}
