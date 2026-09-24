import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { useBindingNames } from "./use-binding-names";
import { getAdminUser } from "@/lib/api/account-security";
import { getRoleDetail } from "@/lib/api/rbac-role-page";
import { getCluster } from "@/lib/api/clusters";
import { getProject } from "@/lib/api/projects";
import type { AccessBinding } from "@/types";
import { queryKeys } from "@/lib/query-keys";
const rights = vi.hoisted(() => ({ read: true }));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: () => ({ allowed: rights.read }),
}));
vi.mock("@/lib/api/account-security", () => ({ getAdminUser: vi.fn() }));
vi.mock("@/lib/api/rbac-role-page", () => ({ getRoleDetail: vi.fn() }));
vi.mock("@/lib/api/clusters", () => ({ getCluster: vi.fn() }));
vi.mock("@/lib/api/projects", () => ({ getProject: vi.fn() }));
const rows: AccessBinding[] = [
  {
    id: "b1",
    scope: "project",
    userId: "late-user",
    group: "",
    roleId: "late-role",
    projectId: "late-project",
    createdAt: "2026-09-22T00:00:00Z",
  },
];
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return {
    client,
    ...renderHook(
      () => useBindingNames([...rows, { ...rows[0], id: "b2" }], "project"),
      { wrapper },
    ),
  };
}
beforeEach(() => {
  vi.clearAllMocks();
  rights.read = true;
  vi.mocked(getAdminUser).mockResolvedValue({
    id: "late-user",
    username: "Alice",
  } as never);
  vi.mocked(getRoleDetail).mockResolvedValue({
    id: "late-role",
    name: "Viewer",
  } as never);
  vi.mocked(getProject).mockResolvedValue({
    id: "late-project",
    name: "Production",
  } as never);
});
it("resolves only unique visible IDs instead of capped enumeration", async () => {
  const { result } = setup();
  await waitFor(() => expect(result.current.users).toHaveLength(1));
  expect(getAdminUser).toHaveBeenCalledTimes(1);
  expect(getRoleDetail).toHaveBeenCalledWith(
    "project",
    "late-role",
    expect.any(AbortSignal),
  );
  expect(getProject).toHaveBeenCalledTimes(1);
  expect(getCluster).not.toHaveBeenCalled();
});
it("drops cached names after a denied refresh", async () => {
  const { client, result } = setup();
  await waitFor(() => expect(result.current.users).toHaveLength(1));
  vi.mocked(getAdminUser).mockRejectedValue({ status: 403 });
  await act(() => client.invalidateQueries({ queryKey: queryKeys.users.all }));
  await waitFor(() => expect(result.current.users).toEqual([]));
});
it("does not resolve names without their read permissions", () => {
  rights.read = false;
  const { result } = setup();
  expect(result.current).toEqual({
    users: [],
    roles: [],
    projects: [],
    clusters: [],
  });
  expect(getAdminUser).not.toHaveBeenCalled();
  expect(getRoleDetail).not.toHaveBeenCalled();
  expect(getProject).not.toHaveBeenCalled();
});
