import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
    useNavigate: () => vi.fn(),
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
  };
});

const run = {
  mutate: vi.fn(),
  isPending: false,
  operationState: { phase: "idle" as const },
};

vi.mock("@/components/settings/hooks", () => ({
  useDeleteManagementBackupDestination: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useRunManagementBackupDestination: () => run,
  useTestManagementBackupDestination: () => ({
    mutate: vi.fn(),
    isPending: false,
    operationState: { phase: "idle" },
  }),
  useCreateManagementBackupDestination: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useUpdateManagementBackupDestination: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
  useBackupDrillHistory: () => ({
    data: undefined,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
  useManagementBackupStatus: () => ({
    data: undefined,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
}));

vi.mock("@/components/settings/backup-drill-hooks", () => ({
  useLatestBackupDrill: () => ({
    data: undefined,
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }),
}));

import { DestinationsSection, DestinationModal } from "./-page";

const existingDestination = {
  id: "destination-1",
  name: "DR bucket",
  bucket: "dr",
  enabled: true,
  prefix: "pg",
  region: "us-east-1",
  hasCredentials: true,
};

describe("backup destination form validation", () => {
  it("blocks an empty name or bucket with an inline error", () => {
    render(
      <DestinationModal
        existing={existingDestination as never}
        onClose={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByLabelText(/^Name/), {
      target: { value: "" },
    });
    fireEvent.change(screen.getByLabelText(/^Bucket/), {
      target: { value: "" },
    });

    expect(screen.getByText("Name is required")).toBeInTheDocument();
    expect(screen.getByText("Bucket is required")).toBeInTheDocument();
  });
});

describe("management backup destinations", () => {
  it("runs the selected enabled destination", () => {
    run.mutate.mockClear();
    render(
      <DestinationsSection
        data={
          {
            destinations: [
              {
                id: "destination-1",
                name: "DR bucket",
                bucket: "dr",
                enabled: true,
                prefix: "pg",
                region: "us-east-1",
                hasCredentials: true,
              },
            ],
          } as never
        }
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(run.mutate).toHaveBeenCalledWith("destination-1");
  });

  it("explains a true empty destination configuration", () => {
    render(<DestinationsSection data={{ destinations: [] } as never} />);
    expect(screen.getByText("No backup destinations")).toBeInTheDocument();
  });
});
