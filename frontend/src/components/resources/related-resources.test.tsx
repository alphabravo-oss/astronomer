import { fireEvent, render, screen } from "@testing-library/react";
import { RelatedResources } from "./related-resources";

const state = vi.hoisted(() => ({
  query: {
    data: {} as unknown,
    isError: false,
    isLoading: false,
    isFetching: false,
    error: null as unknown,
    refetch: vi.fn(),
  },
  read: true,
  get: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children }: { to: string; children: React.ReactNode }) => (
    <a href={to}>{children}</a>
  ),
}));
vi.mock("@/lib/permission-hooks", () => ({
  useClusterResourcePermission: () => ({ allowed: state.read }),
}));
vi.mock("@/lib/hooks/kubernetes-proxy", () => ({
  useResourceDiscovery: () => ({
    data: { resources: [], crds: [] },
    isError: false,
    isLoading: false,
    refetch: vi.fn(),
  }),
  useK8sResource: (...args: unknown[]) => {
    state.get(...args);
    return state.query;
  },
}));
const props = {
  clusterId: "c",
  namespace: "app",
  name: "web",
  kind: "Service",
  obj: { spec: { selector: { app: "web" } } },
};
beforeEach(() => {
  vi.clearAllMocks();
  state.read = true;
  state.query = {
    data: {
      items: [
        { metadata: { name: "web-1", labels: { app: "web" } } },
        { metadata: { name: "other", labels: { app: "other" } } },
      ],
      metadata: { continue: "next+/=" },
    },
    isError: false,
    isLoading: false,
    isFetching: false,
    error: null,
    refetch: vi.fn(),
  };
});
it("links only selected namespace Pods and permits bounded continuation", () => {
  render(<RelatedResources {...props} />);
  expect(screen.getByRole("link", { name: "web-1" })).toHaveAttribute(
    "href",
    "/dashboard/clusters/c/pods/app/web-1",
  );
  expect(screen.queryByRole("link", { name: "other" })).not.toBeInTheDocument();
  expect(state.get).toHaveBeenLastCalledWith(
    "c",
    "api/v1/namespaces/app/pods?limit=50",
    true,
  );
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  expect(
    new URLSearchParams(
      (state.get.mock.lastCall?.[1] as string).split("?")[1],
    ).get("continue"),
  ).toBe("next+/=");
});
it("does not enumerate Pods for selectorless Services", () => {
  render(<RelatedResources {...props} obj={{ spec: {} }} />);
  expect(screen.getByText(/no Pod selector/)).toBeVisible();
  expect(state.get).not.toHaveBeenCalled();
});
it("does not disclose cached Pod names after a denied refresh", () => {
  state.query.isError = true;
  state.query.error = { response: { status: 403 } };
  render(<RelatedResources {...props} />);
  expect(screen.queryByRole("link", { name: "web-1" })).not.toBeInTheDocument();
});
it("disables Pod queries when read permission is missing", () => {
  state.read = false;
  render(<RelatedResources {...props} />);
  expect(state.get).toHaveBeenLastCalledWith(
    "c",
    "api/v1/namespaces/app/pods?limit=50",
    false,
  );
  expect(screen.queryByRole("link", { name: "web-1" })).not.toBeInTheDocument();
});
it("retains Previous after a denied later page without exposing cached links", () => {
  const view = render(<RelatedResources {...props} />);
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  state.query.isError = true;
  state.query.error = { status: 403 };
  view.rerender(<RelatedResources {...props} />);
  expect(screen.queryByRole("link", { name: "web-1" })).not.toBeInTheDocument();
  expect(
    screen.queryByText(/No matching relationships/),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  expect(state.get).toHaveBeenLastCalledWith(
    "c",
    "api/v1/namespaces/app/pods?limit=50",
    true,
  );
});
