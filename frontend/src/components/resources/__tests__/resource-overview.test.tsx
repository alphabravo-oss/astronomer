/**
 * Tests for the kind-specific ResourceOverview branches (plan A4 / C1).
 *
 * ResourceOverview is a pure presentational component, so we render it
 * directly with raw-k8s-object fixtures and assert the tailored sections.
 * The full ResourceDetail shell (which pulls in PodTerminal/wterm) is exercised
 * by the Playwright e2e instead.
 */

import React, { type ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// GenericOverview mounts LabelsAnnotationsEditor, which reads/writes through
// react-query (useMutation/useQueryClient) — every render needs a client.
function renderWithQuery(ui: ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>,
  );
}

// resource-detail statically imports PodTerminal, which pulls in the WASM-backed
// @wterm/react bundle. We never render the terminal here, so stub the module
// out. (e2e covers the real terminal path.)
vi.mock("@/components/workloads/pod-terminal", () => ({
  PodTerminal: () => null,
}));
vi.mock("@/lib/permission-hooks", () => ({
  useClusterResourcePermission: () => ({
    allowed: true,
    reason: "Granted for test",
    disabledReason: "",
  }),
  usePermissionDecision: () => ({
    allowed: true,
    reason: "Granted for test",
    disabledReason: "",
  }),
  canonicalPermissionResource: (resourceType: string) => resourceType,
  permissionDeniedReason: () => "Denied for test",
}));
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const original =
    await importOriginal<typeof import("@tanstack/react-router")>();
  return {
    ...original,
    Link: ({ children, to }: { children: React.ReactNode; to: string }) => (
      <a href={to}>{children}</a>
    ),
  };
});

import { ResourceOverview } from "@/components/resources/resource-detail";

describe("ResourceOverview kind-specific branches", () => {
  it("renders a pod overview with phase, node, IP and per-container rows", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="pods"
        obj={{
          metadata: { name: "my-pod", namespace: "default" },
          spec: {
            nodeName: "node-1",
            containers: [{ name: "app", image: "nginx:1.25" }],
          },
          status: {
            phase: "Running",
            podIP: "10.1.2.3",
            containerStatuses: [
              {
                name: "app",
                ready: true,
                restartCount: 2,
                state: { running: {} },
              },
            ],
          },
        }}
      />,
    );

    expect(screen.getByText("Phase")).toBeInTheDocument();
    expect(screen.getAllByText("Running").length).toBeGreaterThan(0);
    expect(screen.getAllByText("node-1").length).toBeGreaterThan(0);
    expect(screen.getByText("10.1.2.3")).toBeInTheDocument();
    // Per-container row: name + image + state.
    expect(screen.getByText("app")).toBeInTheDocument();
    expect(screen.getByText("nginx:1.25")).toBeInTheDocument();
    expect(screen.getAllByText("Running").length).toBeGreaterThan(0);
  });

  it("renders a service overview with type, clusterIP, ports and selector", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="services"
        obj={{
          metadata: { name: "my-svc", namespace: "default" },
          spec: {
            type: "ClusterIP",
            clusterIP: "10.0.0.10",
            selector: { app: "web" },
            ports: [
              { name: "http", port: 80, targetPort: 8080, protocol: "TCP" },
            ],
          },
        }}
      />,
    );

    expect(screen.getByText("Service")).toBeInTheDocument();
    expect(screen.getByText("ClusterIP")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.10")).toBeInTheDocument();
    expect(screen.getByText("Ports")).toBeInTheDocument();
    expect(screen.getByText("8080")).toBeInTheDocument();
    expect(screen.getByText("Selector")).toBeInTheDocument();
    expect(screen.getByText("web")).toBeInTheDocument();
  });

  it("lists configmap keys", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="configmaps"
        obj={{
          metadata: { name: "cm", namespace: "default" },
          data: { "config.yaml": "a: 1", "app.properties": "x=1" },
        }}
      />,
    );

    expect(screen.getByText("Keys")).toBeInTheDocument();
    // Keys appear in both the kind-specific "Keys" list and the generic "Data" table.
    expect(screen.getAllByText("config.yaml").length).toBeGreaterThan(0);
    expect(screen.getAllByText("app.properties").length).toBeGreaterThan(0);
  });

  it("renders ingress class, hosts and TLS", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="ingresses"
        obj={{
          metadata: { name: "ing", namespace: "default" },
          spec: {
            ingressClassName: "nginx",
            rules: [{ host: "app.example.com" }],
            tls: [{ hosts: ["app.example.com"], secretName: "tls" }],
          },
        }}
      />,
    );

    expect(screen.getByText("Ingress")).toBeInTheDocument();
    expect(screen.getByText("nginx")).toBeInTheDocument();
    expect(screen.getByText("Hosts")).toBeInTheDocument();
    // host appears in both the Hosts list and the TLS list.
    expect(screen.getAllByText("app.example.com").length).toBeGreaterThan(0);
    expect(screen.getByText("TLS")).toBeInTheDocument();
  });

  it("renders PVC status, capacity, storageClass and volume", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="persistentvolumeclaims"
        obj={{
          metadata: { name: "data", namespace: "default" },
          spec: { storageClassName: "gp3", volumeName: "pv-1" },
          status: { phase: "Bound", capacity: { storage: "10Gi" } },
        }}
      />,
    );

    expect(screen.getByText("PersistentVolumeClaim")).toBeInTheDocument();
    expect(screen.getByText("Bound")).toBeInTheDocument();
    expect(screen.getByText("10Gi")).toBeInTheDocument();
    expect(screen.getByText("gp3")).toBeInTheDocument();
    expect(screen.getByText("pv-1")).toBeInTheDocument();
  });

  it("renders a job overview with completions, parallelism and counts", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="jobs"
        obj={{
          metadata: { name: "backup", namespace: "default" },
          spec: { completions: 3, parallelism: 2, backoffLimit: 4 } as never,
          status: { succeeded: 1, active: 2, failed: 0 } as never,
        }}
      />,
    );
    expect(screen.getByText("Job")).toBeInTheDocument();
    expect(screen.getByText("completions")).toBeInTheDocument();
    expect(screen.getByText("1/3")).toBeInTheDocument();
    expect(screen.getByText("parallelism")).toBeInTheDocument();
  });

  it("renders a cronjob overview with schedule and suspend state", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="cronjobs"
        obj={{
          metadata: { name: "nightly", namespace: "default" },
          spec: {
            schedule: "0 3 * * *",
            suspend: true,
            concurrencyPolicy: "Forbid",
          } as never,
          status: { active: [{ name: "nightly-123" }] } as never,
        }}
      />,
    );
    expect(screen.getByText("CronJob")).toBeInTheDocument();
    expect(screen.getByText("0 3 * * *")).toBeInTheDocument();
    expect(screen.getByText("suspended")).toBeInTheDocument();
    // active jobs count derived from the status.active array length.
    expect(screen.getByText("active jobs")).toBeInTheDocument();
  });

  it("renders an HPA overview with target, min/max and replicas", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="hpa"
        obj={{
          metadata: { name: "web-hpa", namespace: "default" },
          spec: {
            scaleTargetRef: { kind: "Deployment", name: "web" },
            minReplicas: 2,
            maxReplicas: 10,
            metrics: [
              {
                type: "Resource",
                resource: { name: "cpu", target: { averageUtilization: 75 } },
              },
            ],
          } as never,
          status: { currentReplicas: 3, desiredReplicas: 4 } as never,
        }}
      />,
    );
    expect(screen.getByText("HorizontalPodAutoscaler")).toBeInTheDocument();
    expect(screen.getByText("Deployment web")).toBeInTheDocument();
    expect(screen.getByText("2 / 10")).toBeInTheDocument();
    expect(screen.getByText("Metrics")).toBeInTheDocument();
    expect(screen.getByText("target 75%")).toBeInTheDocument();
  });

  it("renders RBAC role rules", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="k8s-clusterroles"
        obj={
          {
            metadata: { name: "reader" },
            rules: [
              { apiGroups: [""], resources: ["pods"], verbs: ["get", "list"] },
            ],
          } as never
        }
      />,
    );
    expect(screen.getByText("Rules")).toBeInTheDocument();
    expect(screen.getByText("core")).toBeInTheDocument(); // '' apiGroup -> core
    expect(screen.getByText("pods")).toBeInTheDocument();
    expect(screen.getByText("get, list")).toBeInTheDocument();
  });

  it("renders a rolebinding roleRef and subjects", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="k8s-rolebindings"
        obj={
          {
            metadata: { name: "rb", namespace: "default" },
            roleRef: { kind: "ClusterRole", name: "reader" },
            subjects: [
              { kind: "ServiceAccount", name: "sa-1", namespace: "default" },
            ],
          } as never
        }
      />,
    );
    expect(screen.getByText("Role Reference")).toBeInTheDocument();
    expect(screen.getByText("reader")).toBeInTheDocument();
    expect(screen.getByText("Subjects")).toBeInTheDocument();
    expect(screen.getByText("sa-1")).toBeInTheDocument();
  });

  it("renders a storageclass with provisioner and parameters", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="storageclasses"
        obj={
          {
            metadata: { name: "gp3" },
            provisioner: "ebs.csi.aws.com",
            reclaimPolicy: "Delete",
            parameters: { type: "gp3" },
          } as never
        }
      />,
    );
    expect(screen.getByText("StorageClass")).toBeInTheDocument();
    expect(screen.getByText("ebs.csi.aws.com")).toBeInTheDocument();
    expect(screen.getByText("Parameters")).toBeInTheDocument();
  });

  it("surfaces top-level status scalars for an unmapped/custom kind", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="widgets"
        obj={
          {
            metadata: { name: "w1", namespace: "default" },
            status: {
              phase: "Ready",
              observedGeneration: 3,
              conditions: [{ type: "Ready", status: "True" }],
            },
          } as never
        }
      />,
    );
    // Generic Status section renders scalar status fields (not conditions).
    expect(screen.getByText("Status")).toBeInTheDocument();
    expect(screen.getByText("phase")).toBeInTheDocument();
    expect(screen.getByText("Ready")).toBeInTheDocument();
    expect(screen.getByText("observedGeneration")).toBeInTheDocument();
  });

  it("surfaces top-level spec scalars for an unmapped/custom kind", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="widgets"
        obj={
          {
            metadata: { name: "w1", namespace: "default" },
            spec: { size: "large", replicas: 5, template: { nested: true } },
          } as never
        }
      />,
    );
    // Generic Spec section renders scalar spec fields, skipping nested objects
    // and the replicas/paused/suspend fields kind overviews handle.
    expect(screen.getByText("Spec")).toBeInTheDocument();
    expect(screen.getByText("size")).toBeInTheDocument();
    expect(screen.getByText("large")).toBeInTheDocument();
    expect(screen.queryByText("replicas")).not.toBeInTheDocument();
  });

  it("renders a Gateway with class and listeners", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="gateways"
        obj={
          {
            metadata: { name: "gw", namespace: "default" },
            spec: {
              gatewayClassName: "istio",
              listeners: [{ name: "http", port: 80, protocol: "HTTP" }],
            },
            status: { addresses: [{ value: "10.0.0.5" }] },
          } as never
        }
      />,
    );
    expect(screen.getByText("Gateway")).toBeInTheDocument();
    expect(screen.getByText("istio")).toBeInTheDocument();
    expect(screen.getByText("Listeners")).toBeInTheDocument();
    expect(screen.getByText("HTTP")).toBeInTheDocument();
    expect(screen.getByText("10.0.0.5")).toBeInTheDocument();
  });

  it("masks secret data values", () => {
    renderWithQuery(
      <ResourceOverview
        resourceType="secrets"
        obj={{
          metadata: { name: "creds", namespace: "default" },
          data: { password: "c2VjcmV0" },
        }}
      />,
    );

    // The key is shown (in the Secret keys list + masked Data table) but the
    // value is masked, never the raw base64.
    expect(screen.getAllByText("password").length).toBeGreaterThan(0);
    expect(screen.queryByText("c2VjcmV0")).not.toBeInTheDocument();
    expect(screen.getByText("••••••••")).toBeInTheDocument();
  });
});
