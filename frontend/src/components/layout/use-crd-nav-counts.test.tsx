import { useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { getCRDNavCounts } from "@/lib/api/crd-counts";
import { useClusterScopeStore } from "@/lib/cluster-scope";
import { clusterDiscoveryFromDefinitions } from "./cluster-discovery-model";
import { getClusterNavGroups } from "./sidebar-navigation";
import {
  withDiscoveredNavigation,
  withStarredTypes,
} from "./cluster-discovery-navigation";
import { SidebarGroup } from "./sidebar-navigation-view";
import { useVisibleNavGroups } from "./use-visible-nav-groups";
import { useCRDNavCounts } from "./use-crd-nav-counts";

vi.mock("@/lib/api/crd-counts", () => ({ getCRDNavCounts: vi.fn() }));
vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

const discovery = {
  ...clusterDiscoveryFromDefinitions([
    {
      spec: {
        group: "cert-manager.io",
        scope: "Namespaced",
        names: { plural: "certificates", kind: "Certificate" },
        versions: [{ name: "v1", served: true }],
      },
    },
  ]),
  isLoading: false,
  isError: false,
};
const groups = withStarredTypes(
  withDiscoveredNavigation(getClusterNavGroups("c-1"), discovery, "c-1"),
  ["cert-manager.io/certificates"],
);

function Harness({
  label = "More Resources",
  denied = false,
}: {
  label?: string;
  denied?: boolean;
}) {
  const [collapsed, setCollapsed] = useState(true);
  const { visibleGroups, onFlyoutChange } = useVisibleNavGroups(
    collapsed,
    new Set(),
  );
  const counts = useCRDNavCounts(
    "c-1",
    groups,
    { ...discovery, isError: denied },
    visibleGroups,
  );
  return (
    <>
      <button onClick={() => setCollapsed(!collapsed)}>Toggle sidebar</button>
      <SidebarGroup
        group={groups.find((group) => group.label === label)!}
        pathname="/dashboard/clusters/c-1"
        collapsed={collapsed}
        isOpen={false}
        onToggle={() => {}}
        onFlyoutChange={onFlyoutChange}
        counts={counts}
      />
      <output>{JSON.stringify([...visibleGroups])}</output>
    </>
  );
}

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      {children}
    </QueryClientProvider>
  );
}

beforeEach(() => {
  vi.mocked(getCRDNavCounts)
    .mockReset()
    .mockResolvedValue({ "crd:cert-manager.io/certificates": 7 });
  useClusterScopeStore.setState({ namespacesByCluster: { "c-1": ["team-a"] } });
});

it.each(["More Resources", "Starred"])(
  "fetches scoped counts when opening %s directly from a closed rail",
  async (label) => {
    render(<Harness label={label} />, { wrapper });
    expect(getCRDNavCounts).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: label }));
    await waitFor(() => expect(getCRDNavCounts).toHaveBeenCalled());
    await waitFor(() =>
      expect(
        screen.getByRole("link", { name: /Certificate/ }),
      ).toHaveTextContent("Certificate7"),
    );
    expect(getCRDNavCounts).toHaveBeenCalledWith(
      "c-1",
      expect.any(Array),
      ["team-a"],
      expect.any(AbortSignal),
    );
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent("[]"),
    );
    fireEvent.click(screen.getByRole("button", { name: label }));
    fireEvent.click(screen.getByRole("button", { name: "Toggle sidebar" }));
    fireEvent.click(screen.getByRole("button", { name: "Toggle sidebar" }));
    expect(screen.getByRole("status")).toHaveTextContent("[]");
    expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  },
);

it("does not fetch counts from stale discovery after access is denied", () => {
  render(<Harness denied />, { wrapper });
  fireEvent.click(screen.getByRole("button", { name: "More Resources" }));
  expect(getCRDNavCounts).not.toHaveBeenCalled();
  expect(screen.getByRole("link", { name: "Certificate" })).toBeInTheDocument();
});

it.each([undefined, []])(
  "does not query an unresolved or empty namespace scope (%j)",
  (namespaces) => {
    useClusterScopeStore.setState({
      namespacesByCluster:
        namespaces === undefined ? {} : { "c-1": namespaces },
    });
    render(<Harness />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "More Resources" }));
    expect(getCRDNavCounts).not.toHaveBeenCalled();
  },
);
