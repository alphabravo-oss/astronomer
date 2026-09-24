import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Route } from "./index";
import { getCISScan, createCISScan } from "@/lib/api/security-scans";
import type { CISScanDetail } from "@/types";
import { queryKeys } from "@/lib/query-keys";

const state = vi.hoisted(() => ({
  read: true,
  create: true,
  navigate: vi.fn(),
}));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: (_resource: string, verb: string) => ({
    allowed: verb === "read" ? state.read : state.create,
  }),
}));
vi.mock("@/lib/hooks/entity-names", () => ({
  useEntityNames: () => ({ clusters: [] }),
}));
vi.mock("@/lib/live/hooks", () => ({ useLiveQueryInvalidation: vi.fn() }));
vi.mock("@/lib/toast", () => ({
  toastSuccess: vi.fn(),
  toastApiError: vi.fn(),
}));
vi.mock("@/lib/api/security-scans", async (original) => ({
  ...(await original<typeof import("@/lib/api/security-scans")>()),
  getCISScan: vi.fn(),
  createCISScan: vi.fn(),
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  Link: (await import("@/test/router-link")).RouterLinkStub,
  useNavigate: () => state.navigate,
}));
const Page = Route.options.component!;
const scan: CISScanDetail = {
  id: "scan-1",
  clusterId: "cluster-225",
  scanType: "cis-1.8",
  status: "completed",
  findings: [],
  passed: 0,
  failed: 0,
  warned: 0,
  skipped: 0,
  createdAt: "2026-09-22T00:00:00Z",
  updatedAt: "2026-09-22T00:00:00Z",
};
beforeAll(async () => {
  await (
    Page as typeof Page & { preload?: () => Promise<unknown> }
  ).preload?.();
});
beforeEach(() => {
  vi.clearAllMocks();
  state.read = true;
  state.create = true;
  vi.spyOn(Route, "useParams").mockReturnValue({ scanId: scan.id });
  vi.mocked(getCISScan).mockResolvedValue(scan);
});
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <Page />
    </QueryClientProvider>,
  );
  return client;
}
it.each([
  [403, "Permission required"],
  [404, "Scan not found"],
  [500, "Failed to load scan"],
  [0, "Connection unavailable"],
])("renders %s instead of a perpetual skeleton", async (status, title) => {
  vi.mocked(getCISScan).mockRejectedValue({ status });
  mount();
  expect(await screen.findByText(title)).toBeVisible();
  expect(screen.queryByText("Loading CIS scan")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Re-run Scan" }),
  ).not.toBeInTheDocument();
});
it("hides cached findings and actions after a denied refetch", async () => {
  const client = mount();
  await screen.findByRole("button", { name: "Re-run Scan" });
  vi.mocked(getCISScan).mockRejectedValue({ status: 403 });
  await act(async () => {
    await client.invalidateQueries({
      queryKey: queryKeys.cis.scansAll,
    });
  });
  expect(await screen.findByText("Permission required")).toBeVisible();
  expect(screen.queryByText("Passed")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Re-run Scan" }),
  ).not.toBeInTheDocument();
});
it("does not request a scan without read permission", () => {
  state.read = false;
  mount();
  expect(screen.getByText("Permission required")).toBeVisible();
  expect(getCISScan).not.toHaveBeenCalled();
});
it("disables re-run for readers and removes the CSV target while running", async () => {
  state.create = false;
  vi.mocked(getCISScan).mockResolvedValue({ ...scan, status: "running" });
  mount();
  expect(
    await screen.findByRole("button", { name: "Re-run Scan" }),
  ).toBeDisabled();
  expect(screen.getByText("Export CSV").closest("a")).not.toHaveAttribute(
    "href",
  );
});
it("keeps a failed re-run review open, then navigates only after successful retry", async () => {
  vi.mocked(createCISScan)
    .mockRejectedValueOnce({ status: 403 })
    .mockResolvedValueOnce({ ...scan, id: "new-scan" });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Re-run Scan" }));
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Re-run",
    }),
  );
  await waitFor(() => expect(createCISScan).toHaveBeenCalledTimes(1));
  await waitFor(() =>
    expect(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Re-run",
      }),
    ).toBeEnabled(),
  );
  expect(state.navigate).not.toHaveBeenCalled();
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Re-run",
    }),
  );
  await waitFor(() =>
    expect(state.navigate).toHaveBeenCalledWith({
      to: "/dashboard/security/scans/new-scan",
    }),
  );
  expect(createCISScan).toHaveBeenLastCalledWith({
    cluster_id: scan.clusterId,
    profile: scan.scanType,
  });
  expect(getCISScan).toHaveBeenCalledWith(scan.id, expect.any(AbortSignal));
});
