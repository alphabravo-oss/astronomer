import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AnomalyBaselinesPanel } from "./-page";

const mockUseAnomalyBaselines = vi.hoisted(() => vi.fn());

vi.mock("@/lib/hooks/alerting", () => ({
  useAnomalyBaselines: (...args: unknown[]) => mockUseAnomalyBaselines(...args),
}));

describe("AnomalyBaselinesPanel query states", () => {
  it("renders an error state and not the empty-state copy when the baselines query fails", () => {
    mockUseAnomalyBaselines.mockReturnValue({
      data: undefined,
      error: new Error("boom"),
      isError: true,
      isLoading: false,
      refetch: vi.fn(),
    });
    render(<AnomalyBaselinesPanel clusterId="cluster-1" />);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(
      screen.queryByText(/No baselines computed yet/),
    ).not.toBeInTheDocument();
  });

  it("still shows the empty-state copy when the query succeeds with zero rows", () => {
    mockUseAnomalyBaselines.mockReturnValue({
      data: [],
      error: undefined,
      isError: false,
      isLoading: false,
      refetch: vi.fn(),
    });
    render(<AnomalyBaselinesPanel clusterId="cluster-1" />);
    expect(screen.getByText(/No baselines computed yet/)).toBeInTheDocument();
  });
});
