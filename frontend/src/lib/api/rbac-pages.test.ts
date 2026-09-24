import * as generated from "./generated/client";
import { getRolePage } from "./rbac-role-page";
import { getBindingPage } from "./rbac-binding-page";

vi.mock("./generated/client", () => ({
  getRbacGlobalRoles: vi.fn(),
  getRbacClusterRoles: vi.fn(),
  getRbacProjectRoles: vi.fn(),
  getRbacGlobalRoleBindings: vi.fn(),
  getRbacClusterRoleBindings: vi.fn(),
  getRbacProjectRoleBindings: vi.fn(),
}));
const pagination = {
  limit: 25,
  offset: 225,
  has_more: false,
  next_offset: null,
};
const scopes = ["global", "cluster", "project"] as const;
it("keeps the project filter on later member pages", async () => {
  vi.mocked(generated.getRbacProjectRoleBindings).mockResolvedValue({
    data: [],
    pagination,
  });
  const signal = new AbortController().signal;
  await getBindingPage("project", 225, signal, "project-late");
  expect(generated.getRbacProjectRoleBindings).toHaveBeenLastCalledWith({
    query: { limit: 25, offset: 225, project_id: "project-late" },
    signal,
  });
});
it.each(scopes)(
  "preserves %s role continuation and wire mapping",
  async (scope) => {
    const list =
      scope === "global"
        ? generated.getRbacGlobalRoles
        : scope === "cluster"
          ? generated.getRbacClusterRoles
          : generated.getRbacProjectRoles;
    vi.mocked(list).mockResolvedValue({
      data: [
        {
          id: "r",
          name: "viewer",
          display_name: "Viewer",
          created_at: "2026-09-22T00:00:00Z",
        },
      ],
      pagination,
    });
    const signal = new AbortController().signal;
    expect(await getRolePage(scope, 225, signal)).toMatchObject({
      data: [{ id: "r", displayName: "Viewer" }],
      pagination,
    });
    expect(list).toHaveBeenLastCalledWith({
      query: { limit: 25, offset: 225 },
      signal,
    });
  },
);
it.each(scopes)(
  "preserves %s binding continuation and subject/scope",
  async (scope) => {
    const list =
      scope === "global"
        ? generated.getRbacGlobalRoleBindings
        : scope === "cluster"
          ? generated.getRbacClusterRoleBindings
          : generated.getRbacProjectRoleBindings;
    vi.mocked(list).mockResolvedValue({
      data: [
        {
          id: "b",
          role_id: "r",
          user_id: "u",
          created_at: "2026-09-22T00:00:00Z",
        },
      ],
      pagination,
    });
    const signal = new AbortController().signal;
    expect(await getBindingPage(scope, 225, signal)).toMatchObject({
      data: [{ id: "b", userId: "u", roleId: "r", scope }],
      pagination,
    });
    expect(list).toHaveBeenLastCalledWith({
      query: { limit: 25, offset: 225 },
      signal,
    });
  },
);
