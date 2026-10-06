import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  CappedChips,
  RelativeTime,
  TwoLine,
  capChips,
  shortRelative,
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

describe("shortRelative", () => {
  const now = Date.parse("2026-10-06T12:00:00Z");
  it.each([
    ["2026-10-06T11:59:50Z", "just now"],
    ["2026-10-06T11:48:00Z", "12m ago"],
    ["2026-10-06T09:00:00Z", "3h ago"],
    ["2026-09-28T12:00:00Z", "8d ago"],
    ["2026-05-06T12:00:00Z", "5mo ago"],
    ["2024-10-06T12:00:00Z", "2y ago"],
    ["2026-10-06T12:10:00Z", "in 10m"],
  ])("formats %s as %s", (value, expected) => {
    expect(shortRelative(value, now)).toBe(expected);
  });
  it("treats missing and zero timestamps as undefined", () => {
    expect(shortRelative(null, now)).toBeUndefined();
    expect(shortRelative("0001-01-01T00:00:00Z", now)).toBeUndefined();
    expect(shortRelative("not a date", now)).toBeUndefined();
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
    expect(screen.getByText("1h ago")).toBeTruthy();
  });
});

describe("TwoLine", () => {
  it("renders both lines", () => {
    render(<TwoLine primary="one" secondary="two" />);
    expect(screen.getByText("one")).toBeTruthy();
    expect(screen.getByText("two")).toBeTruthy();
  });
});
