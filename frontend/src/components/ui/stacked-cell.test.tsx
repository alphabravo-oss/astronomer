import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgeCell, exactTimestamp } from "@/components/ui/age-cell";
import { StackedCell } from "@/components/ui/stacked-cell";

describe("StackedCell", () => {
  it("renders primary and secondary lines", () => {
    render(<StackedCell primary="prod-east" secondary="cluster-1" />);
    expect(screen.getByText("prod-east")).toBeInTheDocument();
    expect(screen.getByText("cluster-1")).toBeInTheDocument();
  });

  it("omits the secondary line when empty", () => {
    const { container } = render(<StackedCell primary="only" secondary="" />);
    expect(container.querySelectorAll(".truncate")).toHaveLength(1);
  });
});

describe("AgeCell", () => {
  it("shows the placeholder for missing and zero timestamps", () => {
    expect(exactTimestamp(null)).toBeUndefined();
    expect(exactTimestamp("0001-01-01T00:00:00Z")).toBeUndefined();
    render(<AgeCell value={null} />);
    expect(screen.getByText("—")).toBeInTheDocument();
  });

  it("renders relative text for a valid timestamp", () => {
    render(<AgeCell value="2020-01-01T00:00:00Z" />);
    expect(screen.getByText(/ago/)).toBeInTheDocument();
  });
});
