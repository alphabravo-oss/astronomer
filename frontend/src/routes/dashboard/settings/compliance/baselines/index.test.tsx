import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  applyComplianceBaseline,
  getActiveComplianceBaseline,
  listComplianceBaselineApplications,
  listComplianceBaselines,
  revertComplianceBaselineApplication,
  type ComplianceBaselineApplicationView,
  type ComplianceBaselineView,
} from "@/lib/api/settings";
import { Route } from "./index";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

vi.mock("@/components/settings/auth-gate", () => ({
  SettingsAuthGate: ({ children }: { children: ReactNode }) => children,
}));


vi.mock("@/lib/toast", () => ({
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
}));

vi.mock("@/lib/api/settings", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/settings")>()),
  applyComplianceBaseline: vi.fn(),
  getActiveComplianceBaseline: vi.fn(),
  getComplianceBaselineDiff: vi.fn(),
  listComplianceBaselineApplications: vi.fn(),
  listComplianceBaselines: vi.fn(),
  revertComplianceBaselineApplication: vi.fn(),
}));

const BaselinesPage = Route.options.component!;

beforeAll(async () => {
  await (
    BaselinesPage as typeof BaselinesPage & { preload?: () => Promise<unknown> }
  ).preload?.();
});

const baseline: ComplianceBaselineView = {
  id: "baseline-1",
  slug: "soc2",
  name: "SOC 2",
  description: "Security and availability controls",
  version: "1",
  enabled: true,
  active: true,
  spec: {
    audit_retention_days: 365,
    required_totp: true,
    required_smtp: false,
    quota_plans: [],
    alert_rules: [],
  },
};

const application: ComplianceBaselineApplicationView = {
  id: "application-1",
  baselineId: baseline.id,
  baselineSlug: baseline.slug,
  baselineName: baseline.name,
  appliedAt: "2026-09-10T12:00:00Z",
  status: "applied",
  notes: "",
};

describe("compliance baseline confirmations", () => {
  beforeEach(() => {
    vi.mocked(listComplianceBaselines).mockImplementation(async () => [
      { ...baseline },
    ]);
    vi.mocked(listComplianceBaselineApplications).mockResolvedValue([
      application,
    ]);
    vi.mocked(getActiveComplianceBaseline).mockResolvedValue({
      active: application,
    });
    vi.mocked(applyComplianceBaseline).mockResolvedValue({
      application_id: application.id,
      baseline_id: baseline.id,
      slug: baseline.slug,
    });
    vi.mocked(revertComplianceBaselineApplication).mockResolvedValue({
      application_id: application.id,
      status: "reverted",
    });
  });

  it("previews and executes the reversible apply and revert flow", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    });
    render(
      <QueryClientProvider client={client}>
        <BaselinesPage />
      </QueryClientProvider>,
    );

    fireEvent.click(
      await screen.findByRole("button", { name: "Apply baseline" }),
    );
    const applyDialog = screen.getByRole("dialog", {
      name: "Apply compliance baseline",
    });
    expect(applyDialog).toHaveTextContent(
      "The previous settings will be retained as a reversible snapshot.",
    );
    expect(applyDialog).toHaveTextContent(
      "Use Revert on the latest application.",
    );
    fireEvent.click(within(applyDialog).getByRole("button", { name: "Apply" }));

    await waitFor(() =>
      expect(applyComplianceBaseline).toHaveBeenCalledWith("baseline-1"),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Apply compliance baseline" }),
      ).not.toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("button", { name: "Revert" }));
    const revertDialog = screen.getByRole("dialog", {
      name: "Revert baseline application",
    });
    expect(revertDialog).toHaveTextContent(
      "Apply the baseline again if needed.",
    );
    fireEvent.click(
      within(revertDialog).getByRole("button", { name: "Revert" }),
    );

    await waitFor(() =>
      expect(revertComplianceBaselineApplication).toHaveBeenCalledWith(
        "application-1",
      ),
    );
  });
});
