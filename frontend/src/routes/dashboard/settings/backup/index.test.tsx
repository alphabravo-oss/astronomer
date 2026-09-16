import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const run = { mutate: vi.fn(), isPending: false, operationState: { phase: "idle" as const } };

vi.mock("@/components/settings/hooks", () => ({
  useDeleteManagementBackupDestination: () => ({ mutate: vi.fn(), isPending: false }),
  useRunManagementBackupDestination: () => run,
  useTestManagementBackupDestination: () => ({ mutate: vi.fn(), isPending: false, operationState: { phase: "idle" } }),
  useCreateManagementBackupDestination: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateManagementBackupDestination: () => ({ mutate: vi.fn(), isPending: false }),
  useBackupDrillHistory: () => ({ data: undefined, isLoading: false, isError: false, refetch: vi.fn() }),
  useLatestBackupDrill: () => ({ data: undefined, isLoading: false, isError: false, refetch: vi.fn() }),
  useManagementBackupStatus: () => ({ data: undefined, isLoading: false, isError: false, refetch: vi.fn() }),
}));

import { DestinationsSection } from "./index";

describe("management backup destinations", () => {
  it("runs the selected enabled destination", () => {
    run.mutate.mockClear();
    render(
      <DestinationsSection
        data={{
          destinations: [{ id: "destination-1", name: "DR bucket", bucket: "dr", enabled: true, prefix: "pg", region: "us-east-1", hasCredentials: true }],
        } as never}
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
