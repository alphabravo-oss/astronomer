import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { CustomResourceList } from "./custom-resource-list";
import type { ResourcePermissionDecisions } from "@/components/resources/resource-action-policy";
import type { PermissionDecision } from "@/lib/permissions";

const state = vi.hoisted(() => ({
  selection: null as readonly string[] | null | undefined,
  query: {
    data: {} as unknown,
    isError: false,
    isLoading: false,
    isFetching: false,
    error: null as unknown,
    refetch: vi.fn(),
  },
  permissions: {} as ResourcePermissionDecisions,
  getYaml: vi.fn(),
  download: vi.fn(),
  useResource: vi.fn(),
}));
vi.mock("@/lib/cluster-scope", () => ({
  useClusterNamespaceScope: () => ({ selectedNamespaces: state.selection }),
}));
vi.mock("@/components/layout/use-cluster-discovery-nav", () => ({
  useClusterDiscovery: () => ({
    isLoading: false,
    isError: false,
    crdsByGroup: new Map([
      [
        "example.io",
        [
          {
            group: "example.io",
            servedVersions: ["v2"],
            plural: "widgets",
            namespaced: true,
          },
        ],
      ],
    ]),
  }),
}));
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children: React.ReactNode }) => (
    <span>{children}</span>
  ),
}));
vi.mock("@/lib/hooks/kubernetes-proxy", () => ({
  useK8sResource: (...args: unknown[]) => {
    state.useResource(...args);
    return state.query;
  },
}));
vi.mock("@/components/resources/resource-action-policy", () => ({
  useClusterResourcePermissions: () => state.permissions,
}));
vi.mock("@/lib/api/kubernetes-proxy", () => ({
  k8sGetYaml: (...args: unknown[]) => state.getYaml(...args),
}));
vi.mock("@/lib/utils", async (original) => ({
  ...(await original<typeof import("@/lib/utils")>()),
  downloadBlob: (...args: unknown[]) => state.download(...args),
}));
vi.mock("@/components/resources/create-resource-dialog", () => ({
  CreateResourceDialog: ({ initialYaml }: { initialYaml: string }) => (
    <pre data-testid="clone-manifest">{initialYaml}</pre>
  ),
}));
// Exercise the real permission/action/clone/query boundaries, without jsdom
// virtualizer measurements deciding whether the row exists.
vi.mock("@/components/ui/data-table", () => ({
  DataTable: ({
    data,
    columns,
  }: {
    data: { name: string }[];
    columns: {
      key: string;
      accessor: (row: { name: string }) => React.ReactNode;
    }[];
  }) => (
    <div>
      {data.map((row) => (
        <div key={row.name}>
          {columns.map((column) => (
            <div key={column.key}>{column.accessor(row)}</div>
          ))}
        </div>
      ))}
    </div>
  ),
}));

const allowed: PermissionDecision = {
  allowed: true,
  permission: "custom_resources:read",
  scope: { type: "cluster", id: "c" },
  scopeLabel: "cluster",
  reason: "",
  grantedBy: [],
  requestAccessHint: "",
};
const props = {
  clusterId: "c",
  group: "example.io",
  version: "v2",
  plural: "widgets",
};
beforeEach(() => {
  vi.clearAllMocks();
  state.selection = null;
  state.permissions = Object.fromEntries(
    [
      "create",
      "read",
      "update",
      "delete",
      "scale",
      "restart",
      "exec",
      "logs",
      "manage",
    ].map((action) => [action, allowed]),
  ) as unknown as ResourcePermissionDecisions;
  state.query = {
    data: {
      items: [{ metadata: { name: "demo", namespace: "app" } }],
      metadata: { continue: "opaque+/=&" },
    },
    isError: false,
    isLoading: false,
    isFetching: false,
    error: null,
    refetch: vi.fn(),
  };
  state.getYaml.mockResolvedValue(
    "apiVersion: example.io/v2\nkind: Widget\nmetadata:\n  name: demo\n  namespace: app\n  uid: server-id\n  ownerReferences: []\nstatus:\n  phase: Ready\nspec:\n  color: blue\n",
  );
});

it("uses bounded server pages and preserves opaque continuation", () => {
  render(<CustomResourceList {...props} />);
  expect(state.useResource).toHaveBeenLastCalledWith(
    "c",
    "apis/example.io/v2/widgets?limit=50",
    true,
  );
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  const path = state.useResource.mock.lastCall?.[1] as string;
  expect(new URLSearchParams(path.split("?")[1]).get("continue")).toBe(
    "opaque+/=&",
  );
  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  expect(state.useResource).toHaveBeenLastCalledWith(
    "c",
    "apis/example.io/v2/widgets?limit=50",
    true,
  );
});

it("downloads the exact namespaced group/version/plural live path", async () => {
  render(<CustomResourceList {...props} />);
  fireEvent.click(screen.getByLabelText("Open actions menu"));
  fireEvent.click(screen.getByRole("menuitem", { name: "Download YAML" }));
  await waitFor(() => expect(state.download).toHaveBeenCalled());
  expect(state.getYaml).toHaveBeenCalledWith(
    "c",
    "apis/example.io/v2/namespaces/app/widgets/demo",
  );
});

it("clones a cluster-scoped custom kind without guessing a built-in path", async () => {
  state.query.data = { items: [{ metadata: { name: "demo" } }] };
  render(<CustomResourceList {...props} />);
  fireEvent.click(screen.getByLabelText("Open actions menu"));
  fireEvent.click(screen.getByRole("menuitem", { name: "Clone" }));
  const manifest = await screen.findByTestId("clone-manifest");
  expect(state.getYaml).toHaveBeenCalledWith(
    "c",
    "apis/example.io/v2/widgets/demo",
  );
  expect(manifest.textContent).toContain("apiVersion: example.io/v2");
  expect(manifest.textContent).toContain("kind: Widget");
  expect(manifest.textContent).toContain("name: demo-copy");
  expect(manifest.textContent).not.toMatch(/uid:|ownerReferences:|status:/);
});

it("requires create permission for clone while preserving read-only export", () => {
  state.permissions.create = {
    ...allowed,
    allowed: false,
    reason: "read only",
  };
  render(<CustomResourceList {...props} />);
  fireEvent.click(screen.getByLabelText("Open actions menu"));
  expect(screen.getByRole("menuitem", { name: "Clone" })).toBeDisabled();
  expect(screen.getByRole("menuitem", { name: "Download YAML" })).toBeEnabled();
  expect(state.getYaml).not.toHaveBeenCalled();
});

it("hides cached rows and actions after a denied refresh", () => {
  state.query.isError = true;
  state.query.error = { response: { status: 403 } };
  render(<CustomResourceList {...props} />);
  expect(screen.queryByText("demo")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Open actions menu")).not.toBeInTheDocument();
});

it("uses selected namespace in the API path before pagination and resets continuation", () => {
  state.selection = ["team-b"];
  const view = render(<CustomResourceList {...props} />);
  expect(state.useResource).toHaveBeenLastCalledWith(
    "c",
    "apis/example.io/v2/namespaces/team-b/widgets?limit=50",
    true,
  );
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  state.selection = ["team-a"];
  view.rerender(<CustomResourceList {...props} />);
  expect(state.useResource).toHaveBeenLastCalledWith(
    "c",
    "apis/example.io/v2/namespaces/team-a/widgets?limit=50",
    true,
  );
});
it.each([undefined, [], ["a", "b"]])(
  "does not issue an unscoped query for unresolved/unsupported scope %s",
  (selection) => {
    state.selection = selection;
    render(<CustomResourceList {...props} />);
    expect(state.useResource).not.toHaveBeenCalled();
  },
);
