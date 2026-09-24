import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ProjectMembersCard } from "../members-card";
import { getBindingPage } from "@/lib/api/rbac-binding-page";
import { queryKeys } from "@/lib/query-keys";

const rights = vi.hoisted(() => ({ read: true }));
const mutate = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api/rbac-binding-page", () => ({ getBindingPage: vi.fn() }));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: () => ({ allowed: rights.read }),
  permissionDeniedReason: () => undefined,
}));
vi.mock("@/lib/hooks/rbac", () => ({
  useDeleteAccessBinding: () => ({ mutate, isPending: false }),
}));
vi.mock("@/components/rbac/create-binding-modal", () => ({
  CreateClusterBindingModal: () => null,
}));
vi.mock("@/components/rbac/use-binding-names", () => ({
  useBindingNames: () => ({ users: [], roles: [] }),
}));

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = (id: string) => (
    <QueryClientProvider client={client}>
      <ProjectMembersCard projectId={id} />
    </QueryClientProvider>
  );
  const rendered = render(view("p1"));
  return { client, update: (id = "p1") => rendered.rerender(view(id)) };
}
beforeEach(() => {
  rights.read = true;
  vi.clearAllMocks();
  vi.mocked(getBindingPage).mockImplementation(
    async (_scope, offset, _signal, projectId) => ({
      data: [
        {
          id: `${projectId}-${offset}`,
          scope: "project",
          userId: `user-${offset}`,
          group: "",
          roleId: "role",
          projectId,
          createdAt: "",
        },
      ],
      pagination: {
        limit: 25,
        offset,
        total: 226,
        has_more: offset < 225,
        next_offset: offset < 225 ? offset + 25 : null,
      },
    }),
  );
});
it("pages beyond 200, removes the exact binding, and resets when the project changes", async () => {
  const { update } = mount();
  for (let i = 1; i <= 9; i++) {
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(getBindingPage).toHaveBeenCalledWith(
        "project",
        i * 25,
        expect.any(AbortSignal),
        "p1",
      ),
    );
  }
  fireEvent.click(
    await screen.findByRole("button", { name: "Remove user-225" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));
  expect(mutate).toHaveBeenCalledWith(
    { scope: "project", id: "p1-225" },
    expect.anything(),
  );
  update("p2");
  await waitFor(() =>
    expect(getBindingPage).toHaveBeenCalledWith(
      "project",
      0,
      expect.any(AbortSignal),
      "p2",
    ),
  );
  expect(await screen.findByText("226 role bindings · Page 1")).toBeVisible();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
it.each(["server", "permission"])(
  "hides cached members and counts on %s denial",
  async (denial) => {
    const { client, update } = mount();
    await screen.findByText("user-0");
    if (denial === "server") {
      vi.mocked(getBindingPage).mockRejectedValue({ status: 403 });
      await act(() =>
        client.invalidateQueries({
          queryKey: queryKeys.rbac.projectBindingPage("p1", 0),
        }),
      );
    } else {
      rights.read = false;
      update();
    }
    expect(
      await screen.findByText(
        "You do not have permission to view this project's members.",
      ),
    ).toBeVisible();
    expect(screen.queryByText("user-0")).not.toBeInTheDocument();
    expect(screen.queryByText(/226 role bindings/)).not.toBeInTheDocument();
  },
);
it("does not request a roster without read permission", () => {
  rights.read = false;
  mount();
  expect(getBindingPage).not.toHaveBeenCalled();
});
