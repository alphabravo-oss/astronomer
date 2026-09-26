import { catalogInstallDefaultValues } from "@/lib/catalog-install-defaults";
import { parseHelmValuesYAML } from "@/lib/helm-values-schema";

it("keeps upstream values while disabling duplicate monitoring controllers", () => {
  const result = parseHelmValuesYAML(
    catalogInstallDefaultValues(
      "kube-prometheus-stack",
      "prometheus:\n  prometheusSpec:\n    retention: 15d\nprometheusOperator:\n  enabled: true\nnodeExporter:\n  enabled: true\nkubeStateMetrics:\n  enabled: true\n",
    ),
  );

  expect(result).toMatchObject({
    prometheus: { prometheusSpec: { retention: "15d" } },
    prometheusOperator: { enabled: false },
    nodeExporter: { enabled: false },
    kubeStateMetrics: { enabled: false },
  });
});

it("does not alter charts without an Astronomer compatibility profile", () => {
  expect(catalogInstallDefaultValues("grafana", "replicas: 2\n")).toBe(
    "replicas: 2",
  );
});

it("does not rewrite malformed upstream YAML", () => {
  expect(catalogInstallDefaultValues("kube-prometheus-stack", "[invalid")).toBe(
    "[invalid",
  );
});
