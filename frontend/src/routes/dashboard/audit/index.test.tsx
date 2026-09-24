import { useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { Route } from "./index";
import { getAuditLogs } from "@/lib/api/audit";
import { getAdminUser } from "@/lib/api/account-security";
import { getProjects, getProject } from "@/lib/api/projects";
import { getClusters, getCluster } from "@/lib/api/clusters";

const rights = vi.hoisted(() => ({ read: true, lookup: true }));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: (resource: string) => ({
    allowed: resource === "audit" ? rights.read : rights.lookup,
  }),
}));
vi.mock("@/lib/use-search-param", () => ({
  useSearchParam: () => useState(""),
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  Link: (await import("@/test/router-link")).RouterLinkStub,
}));
vi.mock("@/lib/api/audit", async (original) => ({
  ...(await original<typeof import("@/lib/api/audit")>()),
  getAuditLogs: vi.fn(),
}));
vi.mock("@/lib/api/account-security", () => ({ getAdminUser: vi.fn() }));
vi.mock("@/lib/api/projects", () => ({
  getProjects: vi.fn(),
  getProject: vi.fn(),
}));
vi.mock("@/lib/api/clusters", () => ({
  getClusters: vi.fn(),
  getCluster: vi.fn(),
}));

const Page = Route.options.component!;
beforeAll(async () => {
  await (
    Page as typeof Page & { preload?: () => Promise<unknown> }
  ).preload?.();
});
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrap = (child: ReactNode) => (
    <QueryClientProvider client={client}>{child}</QueryClientProvider>
  );
  const view = render(wrap(<Page />));
  return { client, update: () => view.rerender(wrap(<Page />)) };
}
beforeEach(() => {
  vi.clearAllMocks();
  rights.read = true;
  rights.lookup = true;
  vi.mocked(getAuditLogs).mockImplementation(
    async (params) =>
      ({
        data: [
          {
            id: "a1",
            userId: "user-late",
            user: "operator",
            action: "cluster.update",
            timestamp: "2026-09-22T00:00:00Z",
            status: "success",
          },
        ],
        pagination: {
          limit: 50,
          offset: params?.offset ?? 0,
          total: 101,
          has_more: (params?.offset ?? 0) < 100,
          next_offset: 50,
        },
      }) as never,
  );
  vi.mocked(getAdminUser).mockResolvedValue({
    id: "user-late",
    displayName: "Late actor",
  } as never);
  vi.mocked(getProject).mockImplementation(
    async (id) => ({ id, name: "Late project" }) as never,
  );
  vi.mocked(getCluster).mockImplementation(
    async (id) => ({ id, name: "Late cluster" }) as never,
  );
  vi.mocked(getProjects).mockImplementation(
    async (params) =>
      ({
        data: [
          { id: `p-${params?.page}`, name: `Project page ${params?.page}` },
        ],
        pagination: {
          limit: 25,
          offset: ((params?.page ?? 1) - 1) * 25,
          total: 226,
          has_more: true,
          next_offset: 25,
        },
      }) as never,
  );
});
it("resolves only visible actors and selected scopes; ID filters reset the audit page", async () => {
  setup();
  await screen.findByText("Late actor");
  expect(getAdminUser).toHaveBeenCalledTimes(1);
  expect(getAdminUser).toHaveBeenCalledWith("user-late", {
    signal: expect.any(AbortSignal),
  });
  expect(getProjects).not.toHaveBeenCalled();
  expect(getClusters).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  await waitFor(() =>
    expect(getAuditLogs).toHaveBeenLastCalledWith(
      expect.objectContaining({ offset: 50 }),
      expect.any(AbortSignal),
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Filters" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Project ID" }), {
    target: { value: "late-project" },
  });
  await waitFor(() =>
    expect(getAuditLogs).toHaveBeenLastCalledWith(
      expect.objectContaining({ offset: 0, project_id: "late-project" }),
      expect.any(AbortSignal),
    ),
  );
  await screen.findByText("Project: Late project");
  expect(getProjects).not.toHaveBeenCalled();
  expect(getClusters).not.toHaveBeenCalled();
});
it("selects a project beyond the old 200-target cap", async () => {
  setup();
  await screen.findByText("Late actor");
  fireEvent.click(screen.getByRole("button", { name: "Filters" }));
  fireEvent.click(screen.getByRole("button", { name: "Find project" }));
  for (let i = 1; i <= 9; i++) {
    await screen.findByRole("button", { name: `Select Project page ${i}` });
    const next = within(screen.getByRole("dialog")).getByRole("button", {
      name: "Next page",
    });
    fireEvent.click(next);
  }
  fireEvent.click(
    await screen.findByRole("button", { name: "Select Project page 10" }),
  );
  await waitFor(() =>
    expect(getAuditLogs).toHaveBeenLastCalledWith(
      expect.objectContaining({ project_id: "p-10", offset: 0 }),
      expect.any(AbortSignal),
    ),
  );
});
it("allows known IDs without target/user enumeration permission", async () => {
  rights.lookup = false;
  setup();
  await screen.findByText("operator");
  fireEvent.click(screen.getByRole("button", { name: "Filters" }));
  expect(
    screen.queryByRole("button", { name: "Find project" }),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByRole("textbox", { name: "Cluster ID" }), {
    target: { value: "known-cluster" },
  });
  await waitFor(() =>
    expect(getAuditLogs).toHaveBeenLastCalledWith(
      expect.objectContaining({ cluster_id: "known-cluster" }),
      expect.any(AbortSignal),
    ),
  );
  expect(getAdminUser).not.toHaveBeenCalled();
  expect(getCluster).not.toHaveBeenCalled();
});
it.each(["server", "local"])(
  "hides cached rows and the open detail on %s denial",
  async (denial) => {
    const { client, update } = setup();
    fireEvent.click(await screen.findByText("Late actor"));
    if (denial === "server") {
      vi.mocked(getAuditLogs).mockRejectedValue({ status: 403 });
      await act(() => client.invalidateQueries());
    } else {
      rights.read = false;
      update();
    }
    await waitFor(() =>
      expect(screen.queryByText("Late actor")).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  },
);
