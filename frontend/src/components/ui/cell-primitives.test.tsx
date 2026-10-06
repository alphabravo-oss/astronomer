import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  CappedChips,
  NameSubCell,
  NumberCell,
  TimestampCell,
  formatFixed,
} from "@/components/ui/cell-primitives";

describe("cell primitives", () => {
  it("caps chips at two and summarises the rest", () => {
    render(<CappedChips items={["a=1", "b=2", "c=3", "d=4"]} />);
    expect(screen.getByText("a=1")).toBeTruthy();
    expect(screen.getByText("b=2")).toBeTruthy();
    expect(screen.queryByText("c=3")).toBeNull();
    expect(screen.getByLabelText("2 more: c=3, d=4").textContent).toBe("+2");
  });

  it("renders a dash for empty chip lists and no overflow chip within the cap", () => {
    const { rerender } = render(<CappedChips items={[]} />);
    expect(screen.getByText("—")).toBeTruthy();
    rerender(<CappedChips items={["x", "y"]} />);
    expect(screen.queryByText(/^\+/)).toBeNull();
  });

  it("formats floats at fixed precision", () => {
    expect(formatFixed(1.005 * 100, 2)).toBe("100.50");
    expect(formatFixed(NaN)).toBe("—");
    expect(formatFixed(null)).toBe("—");
    render(<NumberCell value={3} />);
    expect(screen.getByText("3.00")).toBeTruthy();
  });

  it("shows Never without a tooltip for empty timestamps", () => {
    render(<TimestampCell value={null} />);
    expect(screen.getByText("Never")).toBeTruthy();
  });

  it("renders title and subtitle", () => {
    render(<NameSubCell title="Rule" subtitle="desc" />);
    expect(screen.getByText("Rule")).toBeTruthy();
    expect(screen.getByText("desc")).toBeTruthy();
  });
});
