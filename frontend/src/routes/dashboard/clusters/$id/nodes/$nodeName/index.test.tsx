import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import type { PermissionDecision } from "@/lib/permissions";
import type { NodeDetail } from "@/types";
import { NodeDetailPageBody } from "./index";

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
    useNavigate: () => vi.fn(),
    useLocation: <T,>({
      select,
    }: {
      select: (location: { pathname: string; searchStr: string }) => T;
    }) =>
      select({
        pathname: "/dashboard/clusters/cl1/nodes/node-1/",
        searchStr: "",
      }),
  };
});

const mockUseNodeDetail = vi.hoisted(() => vi.fn());
const mockUseNodeOperation = vi.hoisted(() => vi.fn());
vi.mock("@/lib/hooks/clusters", () => ({
  useNodeDetail: (...args: unknown[]) => mockUseNodeDetail(...args),
  useNodeOperation: () => mockUseNodeOperation(),
}));

const allowedDecision: PermissionDecision = {
  allowed: true,
  permission: "nodes:update",
  scope: { type: "cluster", id: "cl1" },
  scopeLabel: "cluster",
  reason: "",
  grantedBy: [],
  requestAccessHint: "",
};
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: (): PermissionDecision => allowedDecision,
  useClusterResourcePermission: (): PermissionDecision => allowedDecision,
}));

function node(overrides: Partial<NodeDetail> = {}): NodeDetail {
  return {
    name: "node-1",
    status: "Ready",
    roles: ["worker"],
    labels: {},
    annotations: {},
    createdAt: new Date().toISOString(),
    nodeInfo: {
      machineId: "m1",
      systemUuid: "u1",
      bootId: "b1",
      kernelVersion: "6.1",
      osImage: "Ubuntu 22.04",
      containerRuntimeVersion: "containerd://1.7",
      kubeletVersion: "v1.29.0",
      kubeProxyVersion: "v1.29.0",
      operatingSystem: "linux",
      architecture: "amd64",
    },
    cpuCapacity: 4,
    cpuUsage: 1,
    memoryCapacity: 16_000_000_000,
    memoryUsage: 4_000_000_000,
    podCapacity: 110,
    podCount: 10,
    addresses: [],
    conditions: [],
    taints: [],
    images: [],
    pods: [],
    events: [],
    unschedulable: false,
    ...overrides,
  };
}

describe("NodeDetailPageBody per-action pending", () => {
  it("does not disable the Add label control while a drain is pending", () => {
    mockUseNodeDetail.mockReturnValue({
      data: node(),
      isLoading: false,
      isError: false,
      refetch: vi.fn(),
    });
    mockUseNodeOperation.mockReturnValue({
      mutateAsync: vi.fn(),
      isPending: true,
      variables: { clusterId: "cl1", nodeName: "node-1", action: "drain" },
      operationState: { phase: "polling" },
    });

    render(<NodeDetailPageBody clusterId="cl1" nodeName="node-1" />, {
      wrapper,
    });

    // The masthead's Drain trigger reflects the pending drain...
    expect(screen.getByRole("button", { name: /drain/i })).toBeDisabled();
    // ...but the unrelated "Add label" control in the Overview tab must stay
    // enabled — this is the bug the per-action pending fix closes.
    expect(screen.getByRole("button", { name: "Add label" })).not.toBeDisabled();
  });
});
