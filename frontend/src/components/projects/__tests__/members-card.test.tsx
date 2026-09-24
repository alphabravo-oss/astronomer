import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

vi.mock("@/lib/toast", () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastApiError: vi.fn(),
  toastWarning: vi.fn(),
}));

vi.mock("@/lib/permission-hooks", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/permission-hooks")>();
  return {
    ...actual,
    usePermissionDecision: () => ({
      allowed: true,
      reason: "",
      disabledReason: "",
    }),
  };
});

const deleteMutate = vi.fn();
const refetch = vi.fn();
const membersQuery = vi.fn();
vi.mock("@/lib/hooks/project-binding-page", () => ({
  useProjectBindingPage: () => {
    const query = membersQuery();
    return {
      ...query,
      data: query.data && {
        data: query.data,
        pagination: {
          limit: 25,
          offset: 0,
          has_more: false,
          next_offset: null,
        },
      },
    };
  },
}));
vi.mock("@/components/rbac/use-binding-names", () => ({
  useBindingNames: () => ({
    users: [{ id: "u1", displayName: "Ada Lovelace", username: "ada" }],
    roles: [{ id: "r1", name: "editor", displayName: "Editor", createdAt: "" }],
  }),
}));
const bindings = [
  {
    id: "b1",
    scope: "project" as const,
    userId: "u1",
    group: "",
    roleId: "r1",
    projectId: "p1",
    createdAt: "2026-01-01T00:00:00Z",
  },
];

vi.mock("@/lib/hooks/rbac", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/hooks/rbac")>();
  return {
    ...actual,
    useProjectRoleBindings: () => membersQuery(),
    useProjectRoles: () => ({
      data: [
        { id: "r1", name: "editor", displayName: "Editor", createdAt: "" },
      ],
    }),
    useDeleteAccessBinding: () => ({
      mutate: deleteMutate,
      isPending: false,
    }),
    // -binding-modal.tsx's own data hooks, so "Add member" can mount it.
    useGlobalRoles: () => ({ data: [] }),
    useClusterRoles: () => ({ data: [] }),
    useRoleTemplates: () => ({ data: [] }),
    useCreateAccessBinding: () => ({
      mutateAsync: vi.fn(),
      isPending: false,
    }),
    useApplyProjectRoleTemplate: () => ({
      mutateAsync: vi.fn(),
      isPending: false,
      error: undefined,
    }),
  };
});

vi.mock("@/lib/hooks/user-settings", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/hooks/user-settings")>();
  return {
    ...actual,
    useUsers: () => ({
      data: {
        data: [{ id: "u1", displayName: "Ada Lovelace", username: "ada" }],
      },
    }),
  };
});

vi.mock("@/lib/hooks/projects", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/hooks/projects")>();
  return {
    ...actual,
    useProjects: () => ({ data: { data: [] } }),
  };
});

import { ProjectMembersCard } from "@/components/projects/members-card";

function wrap(node: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{node}</QueryClientProvider>;
}

describe("ProjectMembersCard", () => {
  beforeEach(() => {
    deleteMutate.mockClear();
    refetch.mockClear();
    membersQuery.mockReturnValue({
      data: bindings,
      isLoading: false,
      isError: false,
      refetch,
    });
  });

  it("renders members from the project-scoped bindings list", () => {
    render(wrap(<ProjectMembersCard projectId="p1" />));
    expect(screen.getByText("Ada Lovelace")).toBeInTheDocument();
    expect(screen.getByText(/editor/i)).toBeInTheDocument();
  });

  it.each([undefined, bindings])(
    "shows denied access, not an empty or stale member list (%j)",
    (data) => {
      membersQuery.mockReturnValue({
        data,
        isLoading: false,
        isError: true,
        error: { status: 403 },
        refetch,
      });
      render(wrap(<ProjectMembersCard projectId="p1" />));
      expect(
        screen.getByText(
          "You do not have permission to view this project's members.",
        ),
      ).toBeInTheDocument();
      expect(screen.queryByText("No members yet.")).not.toBeInTheDocument();
      expect(screen.queryByText("Ada Lovelace")).not.toBeInTheDocument();
    },
  );

  it("offers retry for a transport failure", () => {
    membersQuery.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error("Network unavailable"),
      refetch,
    });
    render(wrap(<ProjectMembersCard projectId="p1" />));
    expect(screen.getByText("Could not load members")).toBeInTheDocument();
    expect(screen.queryByText("No members yet.")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it("shows empty only for a successful empty response", () => {
    membersQuery.mockReturnValue({
      data: [],
      isLoading: false,
      isError: false,
      refetch,
    });
    render(wrap(<ProjectMembersCard projectId="p1" />));
    expect(screen.getByText("No members yet.")).toBeInTheDocument();
  });

  it("Add member opens the binding modal preselected with no scope select", () => {
    render(wrap(<ProjectMembersCard projectId="p1" />));
    fireEvent.click(screen.getByRole("button", { name: "Add member" }));

    expect(screen.getAllByText("Create Binding").length).toBeGreaterThan(0);
    expect(screen.queryByText("Scope")).not.toBeInTheDocument();
    expect(screen.queryByText("Select a project…")).not.toBeInTheDocument();
  });

  it("Remove opens a confirm dialog and revokes the binding on confirm", () => {
    render(wrap(<ProjectMembersCard projectId="p1" />));
    fireEvent.click(
      screen.getByRole("button", { name: "Remove Ada Lovelace" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));

    expect(deleteMutate).toHaveBeenCalledWith(
      { scope: "project", id: "b1" },
      expect.anything(),
    );
  });
});
