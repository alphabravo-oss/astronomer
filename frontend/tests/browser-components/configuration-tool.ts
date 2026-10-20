import type { ClusterTool } from "@/types";
// Synthetic multi-release tool for layout and confirmation checks.
export const tool: ClusterTool = {
  id: "istio",
  slug: "istio",
  name: "Istio",
  description: "Service mesh",
  icon: "network",
  category: "mesh",
  versionConstraint: "1.31.0",
  defaultNamespace: "istio-system",
  isBuiltin: true,
  isEnabled: true,
  charts: [
    {
      chartName: "base",
      repoUrl: "https://example.com",
      namespace: "istio-system",
      order: 0,
      releaseName: "istio-base",
      valuesKey: "base",
    },
    {
      chartName: "istiod",
      repoUrl: "https://example.com",
      namespace: "istio-system",
      order: 1,
      releaseName: "istiod",
      valuesKey: "istiod",
    },
  ],
  presets: { default: {}, development: {} },
  serviceName: "",
  servicePort: null,
  servicePath: "",
  subServices: [],
  createdAt: "2026-09-10",
  updatedAt: "2026-09-10",
  formSchema: {
    fields: [
      {
        path: "istiod.replicaCount",
        label: "Replicas",
        type: "number",
        group: "Control plane",
        default: "2",
        showWhen: { path: "istiod.autoscaleEnabled", equals: "false" },
      },
      {
        path: "istiod.autoscaleEnabled",
        label: "Autoscale",
        type: "boolean",
        group: "Control plane",
        default: "true",
      },
      {
        path: "istiod.telemetry.accessLogFormat",
        label: "Access log format",
        type: "multiline",
        group: "Telemetry",
      },
    ],
  },
};
