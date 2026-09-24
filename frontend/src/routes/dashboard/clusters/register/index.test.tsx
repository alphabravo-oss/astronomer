import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const searchState = vi.hoisted(() => ({ value: {} as { clusterId?: string } }));
const navigateSpy = vi.hoisted(() => vi.fn());
const mockUseCluster = vi.hoisted(() => vi.fn());
const mockUseClusterSearch = vi.hoisted(() => vi.fn());
const createClusterSpy = vi.hoisted(() => vi.fn());
const optionsSpy = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api/clusters", () => ({
  createCluster: createClusterSpy,
  updateCluster: vi.fn(),
}));
vi.mock("@/lib/api/cluster-registration", () => ({
  setRegistrationOptions: optionsSpy,
}));

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const original =
    await importOriginal<typeof import("@tanstack/react-router")>();
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...original,
    Link: RouterLinkStub,
    getRouteApi: () => ({ useSearch: () => searchState.value }),
    useNavigate: () => navigateSpy,
    // FormShell's unsaved-changes guard calls useBlocker, which needs a
    // mounted RouterProvider this test doesn't render; stub it to idle.
    useBlocker: () => ({
      status: "idle" as const,
      current: undefined,
      next: undefined,
      action: undefined,
      proceed: undefined,
      reset: undefined,
    }),
    // The real `createFileRoute(...)(...)` needs a mounted router to back
    // `Route.useSearch()`. This test drives the page component directly, so
    // stub it down to just the pieces the component actually reads —
    // `useSearch` off the shared `searchState`, `options.component` kept for
    // completeness even though the test imports the component by name.
    createFileRoute: () => (routeOptions: Record<string, unknown>) => ({
      useSearch: () => searchState.value,
      options: routeOptions,
    }),
  };
});

vi.mock("@/lib/hooks/clusters", () => ({
  useCluster: (...args: unknown[]) => mockUseCluster(...args),
}));

vi.mock("@/lib/hooks/cluster-search", () => ({
  useClusterSearch: (...args: unknown[]) => mockUseClusterSearch(...args),
}));

vi.mock("@/components/clusters/registration-connect-step", () => ({
  RegistrationConnectStep: ({
    clusterId,
    onBack,
  }: {
    clusterId: string;
    onBack: () => void;
  }) => (
    <div>
      <p>connect-step for {clusterId}</p>
      <button type="button" onClick={onBack}>
        Back
      </button>
    </div>
  ),
}));

import { RegisterClusterWizardRoute } from "./-page";

function draftCluster(overrides: Record<string, unknown> = {}) {
  return {
    id: "abc",
    name: "smoke-east",
    displayName: "smoke-east",
    description: "",
    environment: "development",
    region: "",
    installBaseline: false,
    agentPrivilegeProfile: "viewer",
    apiServerUrl: "",
    caCertificate: "",
    agentOverrides: { resources: {}, proxy: {} },
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  createClusterSpy.mockResolvedValue({ id: "created-cluster" });
  optionsSpy.mockResolvedValue(undefined);
  searchState.value = {};
  mockUseCluster.mockReturnValue({
    data: undefined,
    isError: false,
    isLoading: false,
    refetch: vi.fn(),
  });
  mockUseClusterSearch.mockReturnValue({ data: undefined });
});

describe("register wizard draft identity", () => {
  it("defaults to full management and independent scanning with opt-out", () => {
    render(<RegisterClusterWizardRoute />);
    expect(
      screen.queryByRole("combobox", { name: "Agent privilege profile" }),
    ).not.toBeInTheDocument();
    const baseline = screen.getByRole("checkbox", { name: /Quick Start/ });
    const scanning = screen.getByRole("checkbox", {
      name: /Enable image vulnerability scanning/,
    });
    expect(baseline).toBeChecked();
    expect(scanning).toBeChecked();
    fireEvent.click(scanning);
    expect(scanning).not.toBeChecked();
    expect(baseline).toBeChecked();
    fireEvent.click(scanning);
    fireEvent.click(baseline);
    expect(baseline).not.toBeChecked();
    expect(scanning).toBeEnabled();
    expect(scanning).toBeChecked();
  });

  it("preserves a resumed scanning opt-out", () => {
    searchState.value = { clusterId: "abc" };
    mockUseCluster.mockReturnValue({
      data: draftCluster({
        installBaseline: true,
        agentPrivilegeProfile: "admin",
        annotations: { "astronomer.io/image-scanning": "disabled" },
      }),
      isError: false,
      isLoading: false,
    });
    render(<RegisterClusterWizardRoute />);
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByRole("checkbox", { name: /Quick Start/ })).toBeChecked();
    expect(
      screen.getByRole("checkbox", {
        name: /Enable image vulnerability scanning/,
      }),
    ).not.toBeChecked();
  });

  it("keeps the draft alive across Back — never exits to the cluster list — and lets the operator re-edit their own name", () => {
    searchState.value = { clusterId: "abc" };
    mockUseCluster.mockReturnValue({
      data: draftCluster(),
      isError: false,
      isLoading: false,
      refetch: vi.fn(),
    });

    render(<RegisterClusterWizardRoute />);

    // Step 2 (connect) renders first for a draft that already exists.
    expect(screen.getByText("connect-step for abc")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Back" }));

    // Back must never exit the wizard.
    expect(navigateSpy).not.toHaveBeenCalledWith(
      expect.objectContaining({ to: "/dashboard/clusters" }),
    );
    // It preserves the draft id in the URL it navigates to.
    expect(navigateSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        to: "/dashboard/clusters/register",
        search: { clusterId: "abc" },
      }),
    );

    // Step 1 now renders, prefilled from the draft.
    const nameInput = screen.getByPlaceholderText("my-cluster");
    expect(nameInput).toHaveValue("smoke-east");

    // The operator's own draft name must not report as taken.
    mockUseClusterSearch.mockReturnValue({
      data: { pages: [{ data: [draftCluster()], pagination: {} }] },
    });
    fireEvent.change(nameInput, { target: { value: "smoke-east" } });
    expect(
      screen.queryByText(/already exists\. Choose a different name\./),
    ).not.toBeInTheDocument();
  });

  it("shows a loading state instead of an empty form while the draft cluster is loading", () => {
    searchState.value = { clusterId: "abc" };
    mockUseCluster.mockReturnValue({
      data: undefined,
      isError: false,
      isLoading: true,
      refetch: vi.fn(),
    });

    render(<RegisterClusterWizardRoute />);

    expect(screen.queryByPlaceholderText("my-cluster")).not.toBeInTheDocument();
  });
});

it.each([true, false])(
  "submits scanner enabled=%s independently of the metrics baseline",
  async (scanningEnabled) => {
    render(<RegisterClusterWizardRoute />);
    fireEvent.change(screen.getByPlaceholderText("my-cluster"), {
      target: { value: "remote-cluster" },
    });
    fireEvent.click(screen.getByRole("checkbox", { name: /Quick Start/ }));
    if (!scanningEnabled)
      fireEvent.click(
        screen.getByRole("checkbox", {
          name: /Enable image vulnerability scanning/,
        }),
      );
    fireEvent.click(
      screen.getByRole("button", { name: /Next: Get install command/ }),
    );
    await waitFor(() =>
      expect(optionsSpy).toHaveBeenCalledWith("created-cluster", false),
    );
    expect(createClusterSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        annotations: {
          "astronomer.io/agent-privilege-profile": "admin",
          "astronomer.io/image-scanning": scanningEnabled
            ? "enabled"
            : "disabled",
        },
      }),
    );
  },
);
