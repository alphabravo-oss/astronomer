import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OpenAPIReadComponents } from "@/types/openapi.generated";
import {
  getImageDatabaseStatus,
  getImageVulnReport,
} from "@/lib/api/cluster-vulnerabilities";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { ReportCVEs } from "@/components/security/report-cves";
import { ImageDatabaseStatus } from "./-database-status";

vi.mock("@/lib/api/cluster-vulnerabilities", () => ({
  getImageDatabaseStatus: vi.fn(),
  getImageVulnReport: vi.fn(),
}));
vi.mock("@/lib/permission-hooks", () => ({ usePermissionDecision: vi.fn() }));

type Status = OpenAPIReadComponents["schemas"]["ImageDatabaseStatusV1"];
const clusterId = "52fb8e7d-319e-4d8d-bd0b-7d1b0931a0bf";
const reportId = "58ef5e30-7d56-4a7d-9775-b764d895f154";
const clean: Status = {
  report_id: reportId,
  scan_state: "complete",
  main_state: "fresh",
  java_state: "fresh",
  java_applicability: "needed",
  reason: "fresh",
  finding_count: 0,
  observation_revision: 5,
  witness_revision: 5,
  cycle: 2,
  accepted_generation: 3,
  accepted_feed_digest: `sha256:${"a".repeat(64)}`,
  report_expired: false,
  rescan_required: false,
  eligible_for_clean_posture: true,
  current_workload_checked: true,
  resource_capacity_exhausted: false,
};
const clients: QueryClient[] = [];
function mount(status?: Status, findings = false) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: Infinity },
    },
  });
  clients.push(client);
  if (status)
    client.setQueryData(
      ["image-database-status-v1", clusterId, reportId],
      status,
    );
  return render(
    <QueryClientProvider client={client}>
      <ImageDatabaseStatus clusterId={clusterId} reportId={reportId} />
      {findings && (
        <ReportCVEs clusterId={clusterId} reportId={reportId} severity="" />
      )}
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(usePermissionDecision).mockReturnValue({ allowed: true } as never);
  vi.mocked(getImageDatabaseStatus).mockResolvedValue(clean);
});
afterEach(async () => {
  cleanup();
  for (const client of clients.splice(0)) {
    await client.cancelQueries();
    client.clear();
  }
});

describe("database status clean posture presentation", () => {
  it.each([
    ["needed", "fresh"],
    ["not_needed", "not_needed"],
  ] as const)(
    "shows qualification only for consistent known Java coverage (%s)",
    (java_applicability, java_state) => {
      mount({ ...clean, java_applicability, java_state });
      expect(
        screen.getByText("No findings in the qualified current scan."),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Scan provenance details").closest("details"),
      ).not.toHaveAttribute("open");
    },
  );

  it.each<[string, Partial<Status>]>([
    ["unsupported scan", { scan_state: "unsupported" }],
    ["unsupported main", { main_state: "unsupported" }],
    ["unsupported Java", { java_state: "unsupported" }],
    ["unsupported applicability", { java_applicability: "unsupported" }],
    ["pending", { scan_state: "pending" }],
    ["failed", { scan_state: "failed" }],
    ["unavailable", { scan_state: "unavailable" }],
    ["stale", { main_state: "stale" }],
    ["unknown", { main_state: "unknown" }],
    ["expired feed", { main_state: "feed_expired" }],
    ["unreachable mirror", { main_state: "mirror_unreachable" }],
    ["expired report", { report_expired: true }],
    ["new accepted DB requires rescan", { rescan_required: true }],
    ["unchecked workload", { current_workload_checked: false }],
    ["ineligible", { eligible_for_clean_posture: false }],
    ["Java unknown", { java_applicability: "unknown" }],
    [
      "Java inconsistent",
      { java_applicability: "not_needed", java_state: "fresh" },
    ],
    [
      "ordinary observation invalidates old witness",
      { observation_revision: 6 },
    ],
    ["missing witness revision", { witness_revision: 0 }],
    ["wrong report", { report_id: clusterId }],
    ["missing accepted generation", { accepted_generation: 0 }],
    ["missing cycle", { cycle: 0 }],
    ["missing feed", { accepted_feed_digest: undefined }],
    ["resource exhaustion", { resource_capacity_exhausted: true }],
  ])("keeps %s unqualified despite an eligible flag", (_name, changed) => {
    mount({ ...clean, ...changed });
    expect(
      screen.queryByText("No findings in the qualified current scan."),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/current clean posture is unqualified/),
    ).toBeInTheDocument();
  });

  it("keeps known findings and machine-visible capacity repair visible", () => {
    mount({
      ...clean,
      main_state: "unknown",
      java_applicability: "unknown",
      finding_count: 7,
      current_workload_checked: false,
      resource_capacity_exhausted: true,
    });
    expect(screen.getByText(/7 findings remain reported/)).toBeInTheDocument();
    expect(
      screen.getByText(/Java applicability is unknown/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Current workload identity has not been checked/),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("UID ownership");
  });

  it("hides cached status when cluster read permission is denied and makes no request", () => {
    vi.mocked(usePermissionDecision).mockReturnValue({
      allowed: false,
    } as never);
    mount(clean);
    expect(
      screen.getByText("clusters:read", { exact: false }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("No findings in the qualified current scan."),
    ).not.toBeInTheDocument();
    expect(getImageDatabaseStatus).not.toHaveBeenCalled();
    expect(usePermissionDecision).toHaveBeenCalledWith("clusters", "read", {
      type: "cluster",
      id: clusterId,
    });
  });

  it("retains the real CVE section when the independent database status request fails", async () => {
    vi.mocked(getImageDatabaseStatus).mockRejectedValue(
      new Error("private transport failure"),
    );
    vi.mocked(getImageVulnReport).mockResolvedValue({
      vulnerabilities: {
        data: [
          {
            id: "v1",
            reportId,
            vulnerabilityId: "CVE-2026-12345",
            severity: "HIGH",
            pkgName: "package",
            installedVersion: "1",
            fixedVersion: "2",
            primaryLink: "",
            cvssScore: null,
            title: "Known vulnerability",
            description: "",
          },
        ],
        pagination: { limit: 25, offset: 0, has_more: false },
      },
    } as never);
    mount(undefined, true);
    expect(
      await screen.findByText(/Database provenance unavailable/),
    ).toBeInTheDocument();
    expect(await screen.findByText("CVE-2026-12345")).toBeInTheDocument();
    expect(
      screen.queryByText("private transport failure"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("No findings in the qualified current scan."),
    ).not.toBeInTheDocument();
  });
});
