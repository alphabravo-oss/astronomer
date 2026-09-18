import { beforeEach, describe, expect, it, vi } from "vitest";

const { k8sDelete } = vi.hoisted(() => ({ k8sDelete: vi.fn() }));

vi.mock("@/lib/api/kubernetes-proxy", () => ({ k8sDelete }));

import { deleteExplorerResources } from "@/components/resources/explorer-data-table";

describe("explorer bulk deletion", () => {
  beforeEach(() => k8sDelete.mockReset());

  it("calls the audited object API once per target and preserves partial failures", async () => {
    k8sDelete.mockImplementation(async (...args: unknown[]) => {
      if (String(args[1]).endsWith("/bad")) throw new Error("forbidden");
    });
    const rows = [
      { namespace: "api", name: "good" },
      { namespace: "payments", name: "bad" },
    ];
    const results = await deleteExplorerResources(
      "cluster-1",
      rows,
      {
        path: (row) => `api/v1/namespaces/${row.namespace}/pods/${row.name}`,
        label: (row) => `${row.namespace}/${row.name}`,
      },
      (row) => `${row.namespace}/${row.name}`,
    );

    expect(k8sDelete).toHaveBeenCalledTimes(2);
    expect(k8sDelete).toHaveBeenNthCalledWith(
      1,
      "cluster-1",
      "api/v1/namespaces/api/pods/good",
    );
    expect(results).toEqual([
      { key: "api/good", label: "api/good", ok: true },
      {
        key: "payments/bad",
        label: "payments/bad",
        ok: false,
        error: "forbidden",
      },
    ]);
  });
});
