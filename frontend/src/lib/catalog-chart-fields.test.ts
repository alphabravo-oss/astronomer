import {
  curateHelmValuesSchema,
  hasCuratedHelmValuesSchema,
  recommendedCatalogNamespace,
} from "@/lib/catalog-chart-fields";

it("uses dedicated namespaces for supported operator charts", () => {
  expect(recommendedCatalogNamespace("constellation")).toBe(
    "astronomer-constellation",
  );
  expect(recommendedCatalogNamespace("cert-manager")).toBe(
    "astronomer-cert-manager",
  );
  expect(recommendedCatalogNamespace("cloudnative-pg")).toBe("cnpg-system");
  expect(recommendedCatalogNamespace("unknown-chart")).toBe("default");
});

it("has an explicit overlay for every application in the qualification inventory", () => {
  const applications = [
    "constellation",
    "kube-state-metrics",
    "prometheus-node-exporter",
    "metrics-server",
    "kube-prometheus-stack",
    "grafana",
    "loki",
    "trivy-operator",
    "cert-manager",
    "ingress-nginx",
    "external-secrets",
    "kyverno",
    "longhorn",
    "gatekeeper",
    "fluent-bit",
    "external-dns",
    "velero",
    "opentelemetry-collector",
    "tempo",
    "keda",
    "cloudnative-pg",
  ];
  expect(applications).toHaveLength(21);
  expect(applications.filter(hasCuratedHelmValuesSchema)).toEqual(applications);
});

it("projects supported charts to declared core values without inventing paths", () => {
  const schema = {
    type: "object",
    properties: {
      replicaCount: { type: "integer", default: 1 },
      resources: { type: "object", properties: { limits: { type: "object" } } },
      internalOnly: { type: "string" },
    },
  };
  const curated = curateHelmValuesSchema("cert-manager", schema)!;
  expect(curated.properties).toHaveProperty("replicaCount");
  expect(curated.properties).toHaveProperty("resources");
  expect(curated.properties).not.toHaveProperty("internalOnly");
  expect(curated.properties).not.toHaveProperty("crds");
});

it("keeps the complete schema for charts without a supported overlay", () => {
  const schema = {
    type: "object",
    properties: { custom: { type: "string" } },
  };
  expect(curateHelmValuesSchema("custom-chart", schema)).toBe(schema);
});

it("includes audited optional paths omitted from pinned chart defaults", () => {
  const curated = curateHelmValuesSchema("grafana", {
    type: "object",
    properties: {
      persistence: {
        type: "object",
        properties: { enabled: { type: "boolean" } },
      },
    },
  })!;
  const persistence = curated.properties?.persistence;
  expect(persistence?.properties?.storageClassName).toEqual(
    expect.objectContaining({ type: "string", title: "StorageClass" }),
  );
});

it("adds audited required metadata to a field declared by the chart", () => {
  const curated = curateHelmValuesSchema("opentelemetry-collector", {
    type: "object",
    properties: {
      mode: { type: "string", default: "" },
      image: {
        type: "object",
        properties: { repository: { type: "string", default: "" } },
      },
    },
  })!;

  expect(curated.properties?.mode).toEqual(
    expect.objectContaining({
      enum: ["daemonset", "deployment", "statefulset"],
    }),
  );
  expect(curated.properties?.image?.properties?.repository).toEqual(
    expect.objectContaining({
      minLength: 1,
      title: "Collector image repository",
    }),
  );
});

it("projects Constellation's real operational values and supplies its image tag field", () => {
  const curated = curateHelmValuesSchema("constellation", {
    type: "object",
    properties: {
      highAvailability: {
        type: "object",
        properties: { enabled: { type: "boolean", default: false } },
      },
      api: {
        type: "object",
        properties: { replicas: { type: "integer", default: 2 } },
      },
      scanner: {
        type: "object",
        properties: { enabled: { type: "boolean", default: true } },
      },
      obsolete: { type: "string" },
    },
  })!;

  expect(curated.properties?.image?.properties?.tag).toEqual(
    expect.objectContaining({ type: "string", title: "Image tag" }),
  );
  expect(curated.properties?.highAvailability?.properties?.enabled).toEqual(
    expect.objectContaining({ type: "boolean", default: false }),
  );
  expect(curated.properties?.api?.properties?.replicas).toEqual(
    expect.objectContaining({ type: "integer", default: 2 }),
  );
  expect(curated.properties?.scanner?.properties?.enabled).toEqual(
    expect.objectContaining({ type: "boolean", default: true }),
  );
  expect(curated.properties).not.toHaveProperty("replicaCount");
  expect(curated.properties).not.toHaveProperty("service");
  expect(curated.properties).not.toHaveProperty("obsolete");
});

it("exposes audited kube-prometheus-stack subchart values", () => {
  const curated = curateHelmValuesSchema("kube-prometheus-stack", {
    type: "object",
    properties: { grafana: { type: "object", properties: {} } },
  })!;

  expect(
    curated.properties?.grafana?.properties?.persistence?.properties,
  ).toEqual(
    expect.objectContaining({
      enabled: expect.objectContaining({ type: "boolean" }),
      storageClassName: expect.objectContaining({ type: "string" }),
      size: expect.objectContaining({ type: "string" }),
    }),
  );
  expect(curated.properties?.prometheusOperator?.properties?.enabled).toEqual(
    expect.objectContaining({
      type: "boolean",
      title: "Deploy Prometheus Operator",
    }),
  );
});
