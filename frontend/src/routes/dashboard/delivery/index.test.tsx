import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import * as sourcesApi from "@/lib/api/delivery-sources";
import * as bundlesApi from "@/lib/api/delivery-bundles";
import * as targetsApi from "@/lib/api/delivery-targets";
import * as rolloutsApi from "@/lib/api/delivery-rollouts";
import * as deploymentsApi from "@/lib/api/delivery-deployments";
import { DeliveryOverviewPage } from "./-page";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

vi.mock("@/lib/hooks/auth", () => ({
  useCurrentUser: () => ({ data: { id: "operator" } }),
}));

vi.mock("@/lib/permissions", () => ({
  // Deny the fleet-estate and platform-system permissions so the page
  // renders the per-project overview under test, and grant everything
  // the project overview itself needs.
  can: vi.fn(
    (_user: unknown, resource: string) =>
      resource !== "delivery_inventory" && resource !== "delivery_platform",
  ),
}));

vi.mock("@/lib/live/hooks", () => ({ useLiveQueryInvalidation: vi.fn() }));

vi.mock("@/components/delivery/shared", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/components/delivery/shared")>()),
  DeliveryShell: ({ children }: { children: ReactNode }) => children,
  useDeliveryProjectScope: () => ({
    projectId: "project-1",
    projects: [{ id: "project-1", name: "Team A", displayName: "Team A" }],
    projectQuery: { isLoading: false, isError: false, refetch: vi.fn() },
    setProjectId: vi.fn(),
  }),
}));

vi.mock("@/lib/api/delivery-sources", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/delivery-sources")>()),
  listDeliverySources: vi.fn(),
}));
vi.mock("@/lib/api/delivery-bundles", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/delivery-bundles")>()),
  listComponentBundles: vi.fn(),
}));
vi.mock("@/lib/api/delivery-targets", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/delivery-targets")>()),
  listDeliveryTargets: vi.fn(),
}));
vi.mock("@/lib/api/delivery-rollouts", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/delivery-rollouts")>()),
  listDeliveryRollouts: vi.fn(),
}));
vi.mock("@/lib/api/delivery-deployments", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/delivery-deployments")>()),
  listClusterDeployments: vi.fn(),
}));

function emptyPage() {
  return {
    data: [],
    pagination: {
      total: 0,
      limit: 50,
      offset: 0,
      has_more: false,
      next_offset: null,
    },
  };
}

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <DeliveryOverviewPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(sourcesApi.listDeliverySources).mockResolvedValue(emptyPage());
  vi.mocked(bundlesApi.listComponentBundles).mockResolvedValue(emptyPage());
  vi.mocked(targetsApi.listDeliveryTargets).mockResolvedValue(emptyPage());
  vi.mocked(rolloutsApi.listDeliveryRollouts).mockResolvedValue(emptyPage());
  vi.mocked(deploymentsApi.listClusterDeployments).mockResolvedValue(
    emptyPage(),
  );
});

describe("delivery overview error roll-up", () => {
  it("reports failed datasets and hides zeroed counts when rollouts and deployments fail", async () => {
    vi.mocked(rolloutsApi.listDeliveryRollouts).mockRejectedValue(
      new Error("rollouts down"),
    );
    vi.mocked(deploymentsApi.listClusterDeployments).mockRejectedValue(
      new Error("deployments down"),
    );
    mount();

    expect(
      await screen.findByText("Delivery status unavailable"),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Delivery status unavailable",
    );

    const activeTile = (await screen.findByText("Active (latest 10)")).closest(
      "a",
    );
    const driftedTile = (
      await screen.findByText("Drifted (loaded page)")
    ).closest("a");
    expect(activeTile).not.toBeNull();
    expect(driftedTile).not.toBeNull();
    expect(
      within(activeTile as HTMLElement).queryByText("0", { exact: true }),
    ).not.toBeInTheDocument();
    expect(
      within(driftedTile as HTMLElement).queryByText("0", { exact: true }),
    ).not.toBeInTheDocument();
    expect(
      within(activeTile as HTMLElement).getByText("—"),
    ).toBeInTheDocument();
    expect(
      within(driftedTile as HTMLElement).getByText("—"),
    ).toBeInTheDocument();
  });

  it("shows zero counts and no danger panel when every query succeeds", async () => {
    mount();

    expect(await screen.findByText("Active (latest 10)")).toBeInTheDocument();
    expect(
      screen.queryByText("Delivery status unavailable"),
    ).not.toBeInTheDocument();

    const activeTile = screen.getByText("Active (latest 10)").closest("a");
    const driftedTile = screen.getByText("Drifted (loaded page)").closest("a");
    await waitFor(() => {
      expect(activeTile?.textContent).toContain("0");
      expect(driftedTile?.textContent).toContain("0");
    });
    expect(
      await screen.findByText(/No recent delivery failures/),
    ).toBeInTheDocument();
  });

  it("does not report a false all-clear in Recent operator attention when deployments fail", async () => {
    vi.mocked(deploymentsApi.listClusterDeployments).mockRejectedValue(
      new Error("deployments down"),
    );
    mount();

    await screen.findByText("Delivery status unavailable");

    expect(
      screen.queryByText(/No recent delivery failures/),
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText("unavailable")).toHaveTextContent(
      /unavailable/i,
    );
  });
});
