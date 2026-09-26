import type { HelmValuesSchemaNode } from "@/lib/helm-values-schema";

interface CuratedField {
  path: string;
  title?: string;
  description?: string;
  schema?: HelmValuesSchemaNode;
}

const field = (
  path: string,
  title?: string,
  description?: string,
  schema?: HelmValuesSchemaNode,
): CuratedField => ({ path, title, description, schema });

const resourceRequirements = (
  path: string,
  title = "Resources",
): CuratedField =>
  field(path, title, undefined, {
    type: "object",
    properties: {
      requests: {
        type: "object",
        title: "Requests",
        properties: {
          cpu: { type: "string", title: "CPU request" },
          memory: { type: "string", title: "Memory request" },
        },
      },
      limits: {
        type: "object",
        title: "Limits",
        properties: {
          cpu: { type: "string", title: "CPU limit" },
          memory: { type: "string", title: "Memory limit" },
        },
      },
    },
  });

const persistentVolumeFields = (path: string): CuratedField[] => [
  field(`${path}.storageClassName`, "StorageClass", undefined, {
    type: "string",
  }),
  field(`${path}.accessModes`, "Access modes", undefined, {
    type: "array",
    items: {
      type: "string",
      enum: [
        "ReadWriteOnce",
        "ReadOnlyMany",
        "ReadWriteMany",
        "ReadWriteOncePod",
      ],
    },
  }),
  field(`${path}.resources.requests.storage`, "Requested storage", undefined, {
    type: "string",
  }),
];

// These overlays keep supported Apps focused on the choices an operator most
// often needs. Most paths are rendered only when the selected chart schema or
// values file declares them. The few fallback schemas below cover optional
// values audited directly in the pinned chart templates but omitted from their
// defaults. YAML always retains the complete chart surface and every uncurated
// key.
const CURATED_FIELDS: Record<string, CuratedField[]> = {
  constellation: [
    field(
      "image.tag",
      "Image tag",
      "Release tag used for every Constellation component unless a role overrides it.",
      { type: "string" },
    ),
    field("highAvailability.enabled", "Enable high availability"),
    field("highAvailability.topologyKey", "Topology key"),
    field("highAvailability.whenUnsatisfiable", "Scheduling policy"),
    field("networkPolicies.enabled", "Enable network policies"),
    field("api.replicas", "API replicas"),
    field("scanner.enabled", "Enable scanner"),
    field("scanner.replicas", "Scanner replicas"),
    field("scanner.maxConcurrent", "Concurrent scans per replica"),
    field("scanner.cache.persistent", "Persist scanner cache"),
    field("scanner.cache.size", "Scanner cache size"),
    field("scanner.cache.storageClass", "Scanner cache StorageClass"),
    field("scanner.engines.syft", "Enable Syft"),
    field("scanner.engines.trivy", "Enable Trivy"),
    field("scanner.engines.grype", "Enable Grype"),
    field("runtimeAgent.enabled", "Enable runtime agent"),
    field("runtimeAgent.enforcement.mode", "Runtime enforcement mode"),
    field("runtimeAgent.enforcement.network", "Enforce network activity"),
    field("runtimeAgent.enforcement.dpi", "Enable deep packet inspection"),
    field("admission.enabled", "Enable admission controller"),
    field("admission.replicas", "Admission replicas"),
    field("admission.webhook.enabled", "Enable admission webhook"),
    field("admission.webhook.failurePolicy", "Webhook failure policy"),
    field("admission.webhook.timeoutSeconds", "Webhook timeout"),
    field("frontend.replicas", "Web console replicas"),
    field("ingress.enabled", "Enable Ingress"),
    field("ingress.className", "Ingress class"),
    field("ingress.host", "Ingress host"),
    field("ingress.tls.enabled", "Enable Ingress TLS"),
    field("ingress.tls.secretName", "Ingress TLS Secret"),
    field("postgres.mode", "PostgreSQL mode"),
    field("postgres.existingSecret", "Existing PostgreSQL Secret"),
    field("postgres.embeddedConfig.storage", "Embedded database storage"),
    field("postgres.embeddedConfig.storageClass", "Database StorageClass"),
    field("postgres.cloudnativepg.enabled", "Use CloudNativePG"),
    field("postgres.cloudnativepg.instances", "CloudNativePG instances"),
    field("postgres.cloudnativepg.storage", "CloudNativePG storage"),
    field("tls.certManager.enabled", "Use cert-manager for admission TLS"),
    field("tls.certManager.issuer", "Certificate issuer"),
    field("tls.certManager.issuerKind", "Certificate issuer kind"),
  ],
  "kube-state-metrics": [
    field("replicas"),
    field("autosharding.enabled", "Automatic sharding"),
    field("metricLabelsAllowlist", "Metric label allowlist"),
    field("metricAnnotationsAllowList", "Metric annotation allowlist"),
    field("prometheus.monitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources"),
  ],
  "prometheus-node-exporter": [
    field("hostNetwork", "Use host network"),
    field("hostPID", "Use host PID namespace"),
    field("hostRootFsMount.enabled", "Mount host root filesystem"),
    field("service.port", "Metrics port"),
    field("prometheus.monitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources"),
  ],
  "metrics-server": [
    field("replicas"),
    field("apiService.create", "Create Metrics API service"),
    field("args", "Server arguments"),
    field("serviceMonitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources"),
  ],
  "kube-prometheus-stack": [
    field(
      "prometheusOperator.enabled",
      "Deploy Prometheus Operator",
      "Disable this when the cluster already has a Prometheus Operator managing the same CRDs.",
      { type: "boolean" },
    ),
    field("prometheus.prometheusSpec.scrapeInterval", "Scrape interval"),
    field(
      "prometheus.prometheusSpec.evaluationInterval",
      "Rule evaluation interval",
    ),
    field("prometheus.prometheusSpec.retention", "Prometheus retention"),
    field(
      "prometheus.prometheusSpec.retentionSize",
      "Prometheus retention size",
    ),
    field("prometheus.prometheusSpec.replicas", "Prometheus replicas"),
    field("prometheus.prometheusSpec.shards", "Prometheus shards"),
    field("prometheus.prometheusSpec.enableAdminAPI", "Enable admin API"),
    field(
      "prometheus.prometheusSpec.ignoreNamespaceSelectors",
      "Ignore monitor namespace selectors",
    ),
    ...persistentVolumeFields(
      "prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec",
    ),
    resourceRequirements(
      "prometheus.prometheusSpec.resources",
      "Prometheus resources",
    ),
    field("alertmanager.enabled", "Enable Alertmanager"),
    field("alertmanager.alertmanagerSpec.replicas", "Alertmanager replicas"),
    field("alertmanager.alertmanagerSpec.retention", "Alert retention"),
    field(
      "alertmanager.alertmanagerSpec.configSecret",
      "Existing Alertmanager config Secret",
      "Name of a Secret in the release namespace.",
      { type: "string" },
    ),
    ...persistentVolumeFields(
      "alertmanager.alertmanagerSpec.storage.volumeClaimTemplate.spec",
    ),
    resourceRequirements(
      "alertmanager.alertmanagerSpec.resources",
      "Alertmanager resources",
    ),
    field("grafana.enabled", "Enable Grafana"),
    field("grafana.admin.existingSecret", "Existing Grafana admin Secret"),
    field(
      "grafana.persistence.enabled",
      "Enable Grafana persistence",
      undefined,
      {
        type: "boolean",
      },
    ),
    field(
      "grafana.persistence.storageClassName",
      "Grafana StorageClass",
      undefined,
      { type: "string" },
    ),
    field("grafana.persistence.size", "Grafana volume size", undefined, {
      type: "string",
    }),
    field("nodeExporter.enabled", "Enable Node Exporter"),
    field("kubeStateMetrics.enabled", "Enable kube-state-metrics"),
  ],
  grafana: [
    field("replicas"),
    field("persistence.enabled", "Enable persistence"),
    field("persistence.storageClassName", "StorageClass", undefined, {
      type: "string",
    }),
    field("persistence.size", "Volume size"),
    field("admin.existingSecret", "Existing admin Secret"),
    field("ingress.enabled", "Enable Ingress"),
    field("ingress.hosts", "Ingress hosts"),
    resourceRequirements("resources"),
  ],
  loki: [
    field("deploymentMode", "Deployment mode"),
    field("singleBinary.replicas", "Single binary replicas"),
    field("loki.auth_enabled", "Enable tenant authentication"),
    field("loki.storage.type", "Storage type"),
    field("loki.storage.bucketNames.chunks", "Chunks bucket", undefined, {
      type: "string",
    }),
    field("loki.storage.bucketNames.ruler", "Ruler bucket", undefined, {
      type: "string",
    }),
    field("loki.storage.bucketNames.admin", "Admin bucket", undefined, {
      type: "string",
    }),
    field("gateway.enabled", "Enable gateway"),
    field("monitoring.serviceMonitor.enabled", "Create ServiceMonitor"),
  ],
  "trivy-operator": [
    field("operator.scanJobTimeout", "Scan job timeout"),
    field("operator.scanJobsConcurrentLimit", "Concurrent scan jobs"),
    field("operator.vulnerabilityScannerEnabled", "Vulnerability scanner"),
    field("operator.configAuditScannerEnabled", "Configuration audit scanner"),
    field("trivy.severity", "Reported severities"),
    field("trivy.ignoreUnfixed", "Ignore unfixed vulnerabilities"),
    field("serviceMonitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources"),
  ],
  "cert-manager": [
    field("crds.enabled", "Install CRDs"),
    field("replicaCount", "Controller replicas"),
    field("webhook.replicaCount", "Webhook replicas"),
    field("cainjector.replicaCount", "CA injector replicas"),
    field("prometheus.enabled", "Expose Prometheus metrics"),
    field("startupapicheck.enabled", "Run startup API check"),
    resourceRequirements("resources", "Controller resources"),
  ],
  "ingress-nginx": [
    field("controller.replicaCount", "Controller replicas"),
    field("controller.autoscaling", "Autoscaling"),
    field("controller.service.type", "Service type"),
    field(
      "controller.service.externalTrafficPolicy",
      "External traffic policy",
    ),
    field("controller.ingressClassResource", "IngressClass"),
    field("controller.hostPort.enabled", "Bind host ports"),
    field("controller.metrics.enabled", "Expose metrics"),
    resourceRequirements("controller.resources", "Controller resources"),
  ],
  "external-secrets": [
    field("replicaCount", "Controller replicas"),
    field("installCRDs", "Install CRDs"),
    field("webhook.replicaCount", "Webhook replicas"),
    field("certController.replicaCount", "Certificate controller replicas"),
    field("concurrent", "Concurrent reconciles"),
    field("serviceMonitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources", "Controller resources"),
  ],
  kyverno: [
    field("admissionController.replicas", "Admission controller replicas"),
    field("backgroundController.replicas", "Background controller replicas"),
    field("cleanupController.replicas", "Cleanup controller replicas"),
    field("reportsController.replicas", "Reports controller replicas"),
    field("features.policyReports.enabled", "Policy reports"),
    field(
      "admissionController.serviceMonitor.enabled",
      "Create ServiceMonitor",
    ),
  ],
  longhorn: [
    field("persistence.defaultClass", "Default StorageClass"),
    field("persistence.defaultClassReplicaCount", "Replicas per volume"),
    field("persistence.reclaimPolicy", "Reclaim policy"),
    field("defaultSettings.defaultDataPath", "Default data path"),
    field("longhornUI.replicas", "UI replicas"),
    field("ingress.enabled", "Enable Ingress"),
    field("ingress.host", "Ingress host"),
    field("metrics.serviceMonitor.enabled", "Create ServiceMonitor"),
  ],
  gatekeeper: [
    field("replicas", "Controller replicas"),
    field("auditInterval", "Audit interval"),
    field("constraintViolationsLimit", "Violations retained per constraint"),
    field("enableExternalData", "Enable external data"),
    resourceRequirements("controllerManager.resources", "Controller resources"),
  ],
  "fluent-bit": [
    field("kind", "Workload type"),
    field("replicaCount", "Deployment replicas"),
    field("logLevel", "Log level"),
    field("flush", "Flush interval"),
    field("config.inputs", "Inputs"),
    field("config.filters", "Filters"),
    field("config.outputs", "Outputs"),
    field("serviceMonitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources"),
  ],
  "external-dns": [
    field("provider.name", "DNS provider"),
    field("sources", "Record sources"),
    field("domainFilters", "Domain filters"),
    field("txtOwnerId", "TXT owner ID"),
    field("interval", "Sync interval"),
    field("policy", "Record policy"),
    field("serviceMonitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources"),
  ],
  velero: [
    field("configuration.backupStorageLocation", "Backup storage locations"),
    field("configuration.volumeSnapshotLocation", "Snapshot locations"),
    field("credentials.existingSecret", "Credential Secret"),
    field(
      "backupsEnabled",
      "Enable backups",
      "Requires at least one backup storage location. Disable this and snapshots to run an explicit node-agent-only deployment.",
    ),
    field(
      "deployNodeAgent",
      "Deploy node agent",
      "Runs filesystem data-movement agents on cluster nodes, including in node-agent-only deployments.",
    ),
    field("snapshotsEnabled", "Enable snapshots"),
    field("metrics.serviceMonitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources", "Server resources"),
  ],
  "opentelemetry-collector": [
    field("mode", "Deployment mode", undefined, {
      type: "string",
      enum: ["daemonset", "deployment", "statefulset"],
    }),
    field(
      "image.repository",
      "Collector image repository",
      "Required by this chart. Choose the core or contrib collector image that provides the components used by the pipeline.",
      { type: "string", minLength: 1 },
    ),
    field("replicaCount", "Replicas"),
    field("config.receivers", "Receivers"),
    field("config.processors", "Processors"),
    field("config.exporters", "Exporters"),
    field("config.service.pipelines", "Pipelines"),
    field(
      "ports.metrics.enabled",
      "Expose collector metrics",
      "Publishes the collector's internal telemetry so accepted and rejected data can be verified.",
    ),
    field("ports.metrics.servicePort", "Collector metrics port"),
    field("serviceMonitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("resources"),
  ],
  tempo: [
    field("tempo.retention", "Trace retention"),
    field("tempo.storage", "Trace storage"),
    field("persistence.enabled", "Enable persistence"),
    field("persistence.storageClassName", "StorageClass", undefined, {
      type: "string",
    }),
    field("persistence.size", "Volume size"),
    field("serviceMonitor.enabled", "Create ServiceMonitor"),
    resourceRequirements("tempo.resources", "Tempo resources"),
  ],
  keda: [
    field("crds.install", "Install CRDs"),
    field("operator.replicaCount", "Operator replicas"),
    field("metricsServer.replicaCount", "Metrics server replicas"),
    field("webhooks.replicaCount", "Webhook replicas"),
    field("prometheus.operator.enabled", "Operator metrics"),
    resourceRequirements("resources", "Operator resources"),
  ],
  "cloudnative-pg": [
    field("crds.create", "Install CRDs"),
    field("replicaCount", "Operator replicas"),
    field("monitoring.podMonitorEnabled", "Create PodMonitor"),
    resourceRequirements("resources"),
  ],
};

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

export function hasCuratedHelmValuesSchema(chartName: string): boolean {
  return Object.hasOwn(CURATED_FIELDS, chartName.toLowerCase());
}

function nodeAtPath(
  schema: HelmValuesSchemaNode,
  segments: string[],
): HelmValuesSchemaNode | undefined {
  let node: HelmValuesSchemaNode | undefined = schema;
  for (const segment of segments) node = node?.properties?.[segment];
  return node;
}

function insertNode(
  root: HelmValuesSchemaNode,
  segments: string[],
  source: HelmValuesSchemaNode,
  overlay: CuratedField,
) {
  let cursor = root;
  for (const segment of segments.slice(0, -1)) {
    cursor.properties ??= {};
    cursor.properties[segment] ??= {
      type: "object",
      title: segment,
      properties: {},
    };
    cursor = cursor.properties[segment];
  }
  cursor.properties ??= {};
  const leaf = segments[segments.length - 1];
  cursor.properties[leaf] = {
    ...source,
    ...(overlay.title ? { title: overlay.title } : {}),
    ...(overlay.description ? { description: overlay.description } : {}),
  };
}

function isRenderableField(node: HelmValuesSchemaNode): boolean {
  const type = Array.isArray(node.type)
    ? node.type.find((item) => item !== "null")
    : node.type;
  if (type === "object" || (!type && node.properties))
    return Object.keys(node.properties ?? {}).length > 0;
  return true;
}

export function curateHelmValuesSchema(
  chartName: string,
  schema: HelmValuesSchemaNode | null,
): HelmValuesSchemaNode | null {
  if (!schema) return null;
  const fields = CURATED_FIELDS[chartName.toLowerCase()];
  if (!fields) return schema;
  const projected: HelmValuesSchemaNode = {
    type: "object",
    title: schema.title,
    description: schema.description,
    properties: {},
  };
  for (const overlay of fields) {
    const segments = overlay.path.split(".");
    const discovered = nodeAtPath(schema, segments);
    const source = discovered
      ? { ...discovered, ...overlay.schema }
      : overlay.schema;
    if (source && isRenderableField(source))
      insertNode(projected, segments, source, overlay);
  }
  return Object.keys(projected.properties ?? {}).length > 0
    ? projected
    : schema;
}
