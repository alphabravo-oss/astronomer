import type { PropsWithChildren } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import {
  useClusterResourceCounts,
  useK8sCreateBatch,
} from "@/lib/hooks/kubernetes-proxy";

const k8sCreate = vi.fn();
const getClusterResourceCounts = vi.fn();

vi.mock("@/lib/api/kubernetes-proxy", () => ({
  k8sCreate: (...args: unknown[]) => k8sCreate(...args),
}));

vi.mock("@/lib/api/resource-search", () => ({
  getClusterResourceCounts: (...args: unknown[]) =>
    getClusterResourceCounts(...args),
}));

function wrapper({ children }: PropsWithChildren) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useK8sCreateBatch", () => {
  it("applies in document order and records failures without skipping later objects", async () => {
    const calls: string[] = [];
    k8sCreate.mockImplementation(async (_clusterId: string, path: string) => {
      calls.push(path);
      if (path.includes("services")) throw new Error("service rejected");
      return {};
    });
    const { result } = renderHook(() => useK8sCreateBatch(), { wrapper });

    const items = [
      { id: "0", path: "api/v1/namespaces", body: {}, label: "Namespace/a" },
      {
        id: "1",
        path: "api/v1/namespaces/a/services",
        body: {},
        label: "Service/api",
      },
      {
        id: "2",
        path: "apis/apps/v1/namespaces/a/deployments",
        body: {},
        label: "Deployment/api",
      },
    ];

    let settled;
    await act(async () => {
      settled = await result.current.mutateAsync({
        clusterId: "cluster-a",
        items,
      });
    });

    expect(calls).toEqual(items.map((item) => item.path));
    expect(settled).toMatchObject([
      { id: "0", ok: true },
      { id: "1", ok: false, error: expect.any(Error) },
      { id: "2", ok: true },
    ]);
  });
});

describe("useClusterResourceCounts", () => {
  it("requests one canonical count payload for a resource group and namespace scope", async () => {
    getClusterResourceCounts.mockResolvedValue({
      counts: { pods: 7, deployments: 2 },
    });
    const { result } = renderHook(
      () =>
        useClusterResourceCounts(
          "cluster-a",
          ["pods", "deployments"],
          ["team-b", "team-a"],
        ),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.counts).toEqual({ pods: 7, deployments: 2 });
    expect(getClusterResourceCounts).toHaveBeenCalledWith(
      "cluster-a",
      ["pods", "deployments"],
      ["team-b", "team-a"],
      expect.any(AbortSignal),
    );
  });

  it("does not issue an unscoped request while the selected scope is empty", () => {
    getClusterResourceCounts.mockClear();
    const { result } = renderHook(
      () => useClusterResourceCounts("cluster-a", ["pods"], [], true),
      { wrapper },
    );
    expect(result.current.fetchStatus).toBe("idle");
    expect(getClusterResourceCounts).not.toHaveBeenCalled();
  });
});
