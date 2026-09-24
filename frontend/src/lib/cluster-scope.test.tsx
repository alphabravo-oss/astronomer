import {
  projectInCluster,
  projectSelectionSearch,
} from "./cluster-scope-collection";
import { describe, it, expect } from "vitest";
it("accepts secondary cluster membership", () => {
  expect(
    projectInCluster({ clusterId: "a", clusterIds: ["a", "b"] }, "b"),
  ).toBe(true);
  expect(projectInCluster({ clusterId: "a" }, "b")).toBe(false);
});
describe("project scope transaction", () => {
  it("replaces previous namespaces and dependent pagination atomically", () => {
    const result = projectSelectionSearch(
      new URLSearchParams(
        "project=a&namespaces=old&page=3&selected=x&install=chart",
      ),
      "b",
      ["team-b"],
    );
    expect(result.toString()).toBe("project=b&namespaces=team-b");
  });
  it("fails closed while resolving a project", () => {
    expect(
      projectSelectionSearch(new URLSearchParams(), "b").get("namespaces"),
    ).toBe("");
  });
});

import { act, renderHook, waitFor } from "@testing-library/react";
import { useProjectSelection } from "./cluster-scope-project";
const transactions = vi.hoisted(() => ({
  location: {
    pathname: "/dashboard/clusters/c/apps",
    searchStr: "?project=old&namespaces=old",
  },
  navigate: vi.fn(),
  getProject: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  useLocation: () => transactions.location,
  useNavigate: () => transactions.navigate,
}));
vi.mock("@/lib/api/projects", () => ({
  getProject: (...args: unknown[]) => transactions.getProject(...args),
}));
vi.mock("./toast", () => ({ toastApiError: vi.fn() }));
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((yes) => {
    resolve = yes;
  });
  return { promise, resolve };
}
it("a late project response from another picker cannot overwrite the current selection", async () => {
  transactions.navigate.mockClear();
  const first = deferred<{ clusterId: string; namespaces: string[] }>();
  const second = deferred<{ clusterId: string; namespaces: string[] }>();
  transactions.getProject.mockImplementation((id: string) =>
    id === "a" ? first.promise : second.promise,
  );
  const header = renderHook(() => useProjectSelection("c"));
  const apps = renderHook(() => useProjectSelection("c"));
  act(() => {
    void header.result.current.select("a");
    void apps.result.current.select("b");
  });
  await act(async () => {
    second.resolve({ clusterId: "c", namespaces: ["team-b"] });
    await second.promise;
  });
  await act(async () => {
    first.resolve({ clusterId: "c", namespaces: ["team-a"] });
    await first.promise;
  });
  expect(transactions.navigate.mock.lastCall?.[0].to).toContain(
    "project=b&namespaces=team-b",
  );
  expect(
    transactions.navigate.mock.calls.some(([input]) =>
      input.to.includes("namespaces=team-a"),
    ),
  ).toBe(false);
  await waitFor(() => expect(header.result.current.pending).toBe(false));
});
it("unmount invalidates pending project navigation", async () => {
  transactions.navigate.mockClear();
  const request = deferred<{ clusterId: string; namespaces: string[] }>();
  transactions.getProject.mockReturnValue(request.promise);
  const picker = renderHook(() => useProjectSelection("c"));
  act(() => {
    void picker.result.current.select("a");
  });
  picker.unmount();
  await act(async () => {
    request.resolve({ clusterId: "c", namespaces: ["team-a"] });
    await request.promise;
  });
  expect(transactions.navigate).toHaveBeenCalledTimes(1);
  expect(transactions.navigate.mock.lastCall?.[0].to).toContain("namespaces=");
});
