import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import type { ReactNode } from "react";
import { CISScansTab } from "./cis-scans-tab";
import { ReportCVEs } from "./report-cves";
import { getCISScans } from "@/lib/api/security-scans";
import { getImageVulnReport } from "@/lib/api/cluster-vulnerabilities";
import { queryKeys } from "@/lib/query-keys";

const rights = vi.hoisted(() => ({ read: true }));
vi.mock("@/lib/permission-hooks", () => ({
  usePermissionDecision: () => ({ allowed: rights.read }),
}));
vi.mock("@/lib/hooks/entity-names", () => ({
  useEntityNames: () => ({ clusters: [] }),
}));
vi.mock("@/lib/live/hooks", () => ({
  useLiveQueryInvalidation: () => undefined,
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  Link: (await import("@/test/router-link")).RouterLinkStub,
  useNavigate: () => vi.fn(),
}));
vi.mock("@/lib/api/security-scans", () => ({ getCISScans: vi.fn() }));
vi.mock("@/lib/api/cluster-vulnerabilities", () => ({
  getImageVulnReport: vi.fn(),
}));

function page<T>(data: T[], offset: number, total: number | undefined = 226) {
  return {
    data,
    pagination: {
      limit: 25,
      offset,
      total,
      has_more: offset < 225,
      next_offset: offset < 225 ? offset + 25 : null,
    },
  };
}
const cve = {
  id: "v",
  vulnerabilityId: "CVE-2026-1234",
  severity: "HIGH",
  pkgName: "package",
  installedVersion: "1",
  fixedVersion: "2",
  primaryLink: "https://example.test/cve",
  cvssScore: null,
  title: "Finding",
  description: "",
  reportId: "r1",
};
function mount(child: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrap = (node: ReactNode) => (
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  );
  const view = render(wrap(child));
  return { client, update: (node: ReactNode) => view.rerender(wrap(node)) };
}
beforeEach(() => {
  vi.clearAllMocks();
  rights.read = true;
  vi.mocked(getCISScans).mockImplementation(async (params) => {
    const offset = ((params?.page ?? 1) - 1) * 25;
    return page(
      [
        {
          id: `scan-${offset}`,
          clusterId: `cluster-${offset}`,
          scanType: `cis-${offset}`,
          status: "completed",
          passed: 2,
          failed: 1,
          warned: 0,
          skipped: 0,
          createdAt: "2026-09-22T00:00:00Z",
        },
      ],
      offset,
    ) as never;
  });
  vi.mocked(getImageVulnReport).mockImplementation(
    async (_cluster, _report, opts) =>
      ({
        report: {},
        vulnerabilities: page([cve], opts?.offset ?? 0),
        severityFilter: opts?.severity ?? "",
      }) as never,
  );
});
it("pages CIS history beyond 200 without pretending page summaries/search are global", async () => {
  mount(<CISScansTab />);
  await screen.findByText("cis-0");
  expect(screen.getByText("Check totals on this page")).toBeVisible();
  expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
  for (let i = 1; i <= 9; i++) {
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findByText(`cis-${i * 25}`);
  }
  expect(getCISScans).toHaveBeenLastCalledWith(
    { page: 10, pageSize: 25 },
    expect.any(AbortSignal),
  );
  expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
});
it("hides CIS rows and aggregates after denied refetch", async () => {
  const { client } = mount(<CISScansTab />);
  await screen.findByText("cis-0");
  vi.mocked(getCISScans).mockRejectedValue({ status: 403 });
  await act(() =>
    client.invalidateQueries({ queryKey: queryKeys.cis.scansAll }),
  );
  expect(await screen.findByText(/Permission required/)).toBeVisible();
  expect(screen.queryByText("cis-0")).not.toBeInTheDocument();
  expect(
    screen.queryByText("Check totals on this page"),
  ).not.toBeInTheDocument();
});
it("pages CVEs beyond 100 and resets on report, severity and cluster changes", async () => {
  const { update } = mount(
    <ReportCVEs clusterId="c1" reportId="r1" severity="" />,
  );
  await screen.findByText(cve.vulnerabilityId);
  for (let i = 1; i <= 5; i++) {
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() =>
      expect(getImageVulnReport).toHaveBeenLastCalledWith(
        "c1",
        "r1",
        { severity: undefined, limit: 25, offset: i * 25 },
        expect.any(AbortSignal),
      ),
    );
  }
  for (const [clusterId, reportId, severity] of [
    ["c1", "r1", "HIGH"],
    ["c1", "r2", "HIGH"],
    ["c2", "r2", "HIGH"],
  ] as const) {
    update(
      <ReportCVEs
        clusterId={clusterId}
        reportId={reportId}
        severity={severity}
      />,
    );
    await waitFor(() =>
      expect(getImageVulnReport).toHaveBeenLastCalledWith(
        clusterId,
        reportId,
        { severity, limit: 25, offset: 0 },
        expect.any(AbortSignal),
      ),
    );
  }
});
it("labels a CVE lower bound without claiming an exact total", async () => {
  vi.mocked(getImageVulnReport).mockResolvedValue({
    report: {},
    vulnerabilities: {
      data: [cve],
      pagination: { limit: 25, offset: 0, has_more: true, next_offset: 25 },
    },
    severityFilter: "",
  } as never);
  mount(<ReportCVEs clusterId="c1" reportId="r1" severity="" />);
  expect(
    await screen.findByText("At least 2 CVEs matching filter"),
  ).toBeVisible();
  expect(screen.getByRole("button", { name: "Next page" })).toBeEnabled();
});
it.each(["server", "local"])(
  "hides cached CVEs after %s denial",
  async (denial) => {
    const view = <ReportCVEs clusterId="c1" reportId="r1" severity="" />;
    const { client, update } = mount(view);
    await screen.findByText(cve.vulnerabilityId);
    if (denial === "server") {
      vi.mocked(getImageVulnReport).mockRejectedValue({ status: 403 });
      await act(() =>
        client.invalidateQueries({
          queryKey: queryKeys.clusterPages.imageVulnReport("c1", "r1", ""),
        }),
      );
    } else {
      rights.read = false;
      update(<ReportCVEs clusterId="c1" reportId="r1" severity="" />);
    }
    expect(await screen.findByText(/Permission required/)).toBeVisible();
    expect(screen.queryByText(cve.vulnerabilityId)).not.toBeInTheDocument();
    expect(
      screen.queryByText("226 CVEs matching filter"),
    ).not.toBeInTheDocument();
  },
);
it("does not fetch scans or CVEs without read permission", () => {
  rights.read = false;
  mount(
    <>
      <CISScansTab />
      <ReportCVEs clusterId="c1" reportId="r1" severity="" />
    </>,
  );
  expect(getCISScans).not.toHaveBeenCalled();
  expect(getImageVulnReport).not.toHaveBeenCalled();
});
