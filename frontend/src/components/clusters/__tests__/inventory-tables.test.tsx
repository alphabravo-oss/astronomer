import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import {
  WorkloadsTable,
  workloadColumns,
  workloadHref,
  workloadStatus,
  type WorkloadRow,
} from "@/components/clusters/workloads-table";
import {
  EstateClustersTable,
  estateClusterColumns,
} from "@/components/clusters/estate-clusters-table";
import type { Cluster } from "@/types";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});


const deployment: WorkloadRow = {
  kind: "Deployment",
  item: {
    apiVersion: "apps/v1",
    kind: "Deployment",
    metadata: {
      name: "api",
      namespace: "production",
      creationTimestamp: "2026-09-09T12:00:00Z",
    },
    spec: { replicas: 3 },
    status: { readyReplicas: 3 },
  },
};

const cronjob: WorkloadRow = {
  kind: "CronJob",
  item: {
    apiVersion: "batch/v1",
    kind: "CronJob",
    metadata: { name: "cleanup", namespace: "operations" },
    spec: { schedule: "0 3 * * *" },
  },
};

const cluster = (overrides: Partial<Cluster>): Cluster =>
  ({
    id: "cluster-1",
    name: "production-us-east",
    displayName: "Production East",
    status: "active",
    kubernetesVersion: "v1.32.2",
    nodeCount: 12,
    podCount: 240,
    cpuPercentage: 42,
    memoryPercentage: 68,
    ...overrides,
  }) as Cluster;

describe("workload inventory table", () => {
  it("normalizes status and builds canonical detail links", () => {
    expect(workloadStatus(deployment).label).toBe("3/3 ready");
    expect(workloadHref("cluster-a", deployment)).toBe(
      "/dashboard/clusters/cluster-a/deployments/production/api",
    );

    const columns = workloadColumns("cluster-a");
    expect(columns.map((column) => column.key)).toEqual([
      "kind",
      "name",
      "namespace",
      "status",
      "age",
    ]);
    expect(columns.find((column) => column.key === "kind")?.filter).toEqual({
      label: "Kind",
    });
    expect(
      columns.find((column) => column.key === "namespace")?.filter,
    ).toEqual({ label: "Namespace" });
  });

  it("searches JSX cells, facets by kind, and drills in from a row", async () => {
    const onRowClick = vi.fn();
    render(
      <WorkloadsTable
        clusterId="cluster-a"
        data={[deployment, cronjob]}
        onRowClick={onRowClick}
      />,
    );

    fireEvent.change(screen.getByPlaceholderText("Search workloads..."), {
      target: { value: "cleanup" },
    });
    await waitFor(() =>
      expect(screen.queryByText("api")).not.toBeInTheDocument(),
    );
    expect(screen.getByText("cleanup")).toHaveAttribute(
      "href",
      "/dashboard/clusters/cluster-a/cronjobs/operations/cleanup",
    );

    fireEvent.change(screen.getByPlaceholderText("Search workloads..."), {
      target: { value: "" },
    });
    await waitFor(() => expect(screen.getByText("api")).toBeInTheDocument());
    fireEvent.click(screen.getByText("production"));
    expect(onRowClick).toHaveBeenCalledWith(deployment);

    fireEvent.click(screen.getByRole("button", { name: "Kind" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "CronJob" }));
    expect(screen.queryByText("api")).not.toBeInTheDocument();
    expect(screen.getByText("cleanup")).toBeInTheDocument();
  });
});

describe("estate cluster table", () => {
  it("uses the common sortable inventory columns", () => {
    expect(estateClusterColumns.map((column) => column.key)).toEqual([
      "name",
      "status",
      "provider",
      "version",
      "nodes",
      "pods",
      "cpu",
      "memory",
    ]);
    const nodes = estateClusterColumns.find((column) => column.key === "nodes");
    expect(nodes?.sortAccessor?.(cluster({ nodeCount: 27 }))).toBe(27);
  });

  it("shows the provider with its distribution as a sublabel", () => {
    render(
      <EstateClustersTable
        clusters={[cluster({ provider: "aws", distribution: "eks" })]}
        loading={false}
        isError={false}
        onRetry={vi.fn()}
        onRowClick={vi.fn()}
      />,
    );
    expect(screen.getByText("AWS")).toBeInTheDocument();
    expect(screen.getByText("Amazon EKS")).toBeInTheDocument();
  });

  it("searches display names and exposes loading failures with retry", async () => {
    const onRetry = vi.fn();
    const onRowClick = vi.fn();
    const { rerender } = render(
      <EstateClustersTable
        clusters={[
          cluster({}),
          cluster({
            id: "cluster-2",
            name: "staging-eu",
            displayName: "Staging Europe",
          }),
        ]}
        loading={false}
        isError={false}
        onRetry={onRetry}
        onRowClick={onRowClick}
      />,
    );

    fireEvent.change(screen.getByPlaceholderText("Search clusters..."), {
      target: { value: "Production East" },
    });
    await waitFor(() =>
      expect(screen.queryByText("Staging Europe")).not.toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("v1.32.2"));
    expect(onRowClick).toHaveBeenCalledWith(
      expect.objectContaining({ id: "cluster-1" }),
    );

    rerender(
      <EstateClustersTable
        clusters={[]}
        loading={false}
        isError
        onRetry={onRetry}
        onRowClick={onRowClick}
      />,
    );
    expect(screen.getByText("Failed to load cluster health.")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
