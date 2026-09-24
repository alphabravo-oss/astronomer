import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Route } from "./index";
import { getRolePage } from "@/lib/api/rbac-role-page";
import { toastError } from "@/lib/toast";

const create = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  Link: (await import("@/test/router-link")).RouterLinkStub,
  useNavigate: () => vi.fn(),
}));
vi.mock("@/components/settings/auth-gate", () => ({
  SettingsAuthGate: ({ children }: { children: ReactNode }) => children,
}));
vi.mock("@/components/settings/hooks", () => ({
  useCreateGroupMapping: () => ({ mutateAsync: create, isPending: false }),
}));
vi.mock("@/components/auth/hooks", () => ({
  useDexConnectors: () => ({ data: [] }),
}));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: () => ({ allowed: true }),
}));
vi.mock("@/lib/api/rbac-role-page", () => ({ getRolePage: vi.fn() }));
vi.mock("@/lib/toast", () => ({ toastError: vi.fn() }));
vi.mock("@/components/projects/remote-project-picker", () => ({
  RemoteProjectPicker: ({ onChange }: { onChange: (id: string) => void }) => (
    <button onClick={() => onChange("project-1")}>Choose project</button>
  ),
}));
vi.mock("@/components/clusters/remote-cluster-picker", () => ({
  RemoteClusterPicker: ({ onChange }: { onChange: (id: string) => void }) => (
    <button onClick={() => onChange("cluster-1")}>Choose cluster</button>
  ),
}));
const Page = Route.options.component!;
beforeAll(async () => {
  await (
    Page as typeof Page & { preload?: () => Promise<unknown> }
  ).preload?.();
});
beforeEach(() => {
  vi.clearAllMocks();
  create.mockResolvedValue({});
  vi.mocked(getRolePage).mockImplementation(async (scope, offset) => ({
    data: Array.from({ length: offset === 0 ? 25 : 1 }, (_, i) => ({
      id: `${scope}-${offset + i}`,
      name: `${scope} role ${offset + i}`,
      displayName: `${scope} role ${offset + i}`,
      createdAt: "",
      rules: [],
    })),
    pagination: {
      limit: 25,
      offset,
      has_more: offset === 0,
      next_offset: offset === 0 ? 25 : null,
    },
  }));
});
it.each(["cluster", "project"])(
  "selects a paged %s role and clears it when scope changes",
  async (scope) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <Page />
      </QueryClientProvider>,
    );
    fireEvent.change(screen.getByLabelText("Group name"), {
      target: { value: "operators" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Role" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Select global role 0" }),
    );
    fireEvent.change(screen.getByLabelText("Scope"), {
      target: { value: scope },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create mapping" }));
    expect(toastError).toHaveBeenCalledWith("Role is required");
    expect(create).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Role" }));
    await screen.findByRole("button", { name: `Select ${scope} role 0` });
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    fireEvent.click(
      await screen.findByRole("button", { name: `Select ${scope} role 25` }),
    );
    fireEvent.click(screen.getByRole("button", { name: `Choose ${scope}` }));
    fireEvent.click(screen.getByRole("button", { name: "Create mapping" }));
    await waitFor(() =>
      expect(create).toHaveBeenCalledWith({
        group_name: "operators",
        scope,
        role_id: `${scope}-25`,
        [`${scope}_id`]: `${scope}-1`,
      }),
    );
    fireEvent.change(screen.getByLabelText("Scope"), {
      target: { value: "global" },
    });
    expect(screen.getByRole("button", { name: "Role" })).toHaveTextContent(
      "Select a global role",
    );
  },
);
