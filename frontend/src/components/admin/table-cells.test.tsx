import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  CappedChips,
  RelativeTime,
  TwoLine,
  capChips,
} from "@/components/admin/table-cells";

describe("capChips", () => {
  it("caps at two by default", () => {
    expect(capChips(["a", "b", "c", "d"])).toEqual({
      visible: ["a", "b"],
      hidden: ["c", "d"],
    });
  });
  it("has no overflow when within the cap", () => {
    expect(capChips(["a"]).hidden).toEqual([]);
  });
});

describe("CappedChips", () => {
  it("renders two chips and a +N overflow", () => {
    render(<CappedChips items={["a", "b", "c", "d", "e"]} />);
    expect(screen.getByText("a")).toBeTruthy();
    expect(screen.getByText("b")).toBeTruthy();
    expect(screen.queryByText("c")).toBeNull();
    expect(screen.getByText("+3")).toBeTruthy();
  });
  it("renders the empty placeholder", () => {
    render(<CappedChips items={[]} empty="None" />);
    expect(screen.getByText("None")).toBeTruthy();
  });
});

describe("RelativeTime", () => {
  it("falls back for missing values", () => {
    render(<RelativeTime value={null} fallback="Never used" />);
    expect(screen.getByText("Never used")).toBeTruthy();
  });
  it("renders a relative string", () => {
    render(
      <RelativeTime value={new Date(Date.now() - 3600_000).toISOString()} />,
    );
    expect(screen.getByText(/ago/)).toBeTruthy();
  });
});

describe("TwoLine", () => {
  it("renders both lines", () => {
    render(<TwoLine primary="one" secondary="two" />);
    expect(screen.getByText("one")).toBeTruthy();
    expect(screen.getByText("two")).toBeTruthy();
  });
});
