import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { PagedBindingsTab } from "./-paged-bindings-tab";
import { getBindingPage } from "@/lib/api/rbac-binding-page";
import { queryKeys } from "@/lib/query-keys";

vi.mock("@/lib/api/rbac-binding-page", () => ({ getBindingPage: vi.fn() }));
vi.mock("@/components/rbac/use-binding-names", () => ({
  useBindingNames: () => ({ users: [], roles: [], clusters: [], projects: [] }),
}));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: () => ({ allowed: true }),
}));
const binding = {
  id: "binding-225",
  scope: "global" as const,
  userId: "user-225",
  group: "",
  roleId: "role-225",
  createdAt: "2026-09-22T00:00:00Z",
};
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const revoke = vi.fn();
  render(
    <QueryClientProvider client={client}>
      <PagedBindingsTab onRevoke={revoke} />
    </QueryClientProvider>,
  );
  return { client, revoke };
}
beforeEach(() => vi.clearAllMocks());
it("reaches bindings beyond 200, revokes the exact row and resets pages across scopes", async () => {
  vi.mocked(getBindingPage).mockImplementation(async (scope, offset) => ({
    data: offset === 225 ? [{ ...binding, scope }] : [],
    pagination: {
      limit: 25,
      offset,
      total: 226,
      has_more: offset < 225,
      next_offset: offset < 225 ? offset + 25 : null,
    },
  }));
  const { revoke } = mount();
  for (let i = 1; i <= 9; i++) {
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(getBindingPage).toHaveBeenCalledWith(
        "global",
        i * 25,
        expect.any(AbortSignal),
      ),
    );
  }
  expect(await screen.findByText("user-225")).toBeVisible();
  fireEvent.click(screen.getByTitle("Revoke binding"));
  expect(revoke).toHaveBeenCalledWith(binding);
  fireEvent.change(screen.getByLabelText("Binding scope"), {
    target: { value: "project" },
  });
  await waitFor(() =>
    expect(getBindingPage).toHaveBeenLastCalledWith(
      "project",
      0,
      expect.any(AbortSignal),
    ),
  );
  expect(screen.queryByText("user-225")).not.toBeInTheDocument();
});
it("does not expose cached bindings after a denied refresh", async () => {
  vi.mocked(getBindingPage).mockResolvedValue({
    data: [binding],
    pagination: { limit: 25, offset: 0, has_more: false, next_offset: null },
  });
  const { client } = mount();
  await screen.findByText("user-225");
  vi.mocked(getBindingPage).mockRejectedValue({ status: 403 });
  await act(() => client.invalidateQueries({ queryKey: queryKeys.rbac.all }));
  expect(await screen.findByText(/permission required/i)).toBeVisible();
  expect(screen.queryByText("user-225")).not.toBeInTheDocument();
});
