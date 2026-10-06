import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AgeCell, compactAge, exactTimestamp } from "@/components/ui/age-cell";
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

  it("formats compact ages", () => {
    const now = Date.parse("2026-10-06T12:00:00Z");
    expect(compactAge("2026-10-06T11:59:30Z", now)).toBe("just now");
    expect(compactAge("2026-10-06T11:15:00Z", now)).toBe("45m ago");
    expect(compactAge("2026-10-05T16:00:00Z", now)).toBe("20h ago");
    expect(compactAge("2026-10-03T12:00:00Z", now)).toBe("3d ago");
    expect(compactAge("2025-10-06T12:00:00Z", now)).toBe("1y ago");
  });

  it("renders relative text for a valid timestamp", () => {
    render(<AgeCell value="2020-01-01T00:00:00Z" />);
    expect(screen.getByText(/ago/)).toBeInTheDocument();
  });
});
