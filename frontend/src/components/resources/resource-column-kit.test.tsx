import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  abbreviateAccessModes,
  ageColumn,
  chipColumn,
  ChipList,
  countColumn,
  exactTimestamp,
  textColumn,
} from "@/components/resources/resource-column-kit";

describe("resource column kit", () => {
  it("caps chips at two and counts the rest", () => {
    render(<ChipList items={["a", "b", "c", "d"]} />);
    expect(screen.getByText("a")).toBeInTheDocument();
    expect(screen.getByText("b")).toBeInTheDocument();
    expect(screen.queryByText("c")).not.toBeInTheDocument();
    expect(screen.getByText("+2")).toBeInTheDocument();
  });

  it("renders a placeholder for an empty chip list", () => {
    render(<ChipList items={[]} empty="none" />);
    expect(screen.getByText("none")).toBeInTheDocument();
  });

  it("abbreviates access modes and keeps unknown values", () => {
    expect(
      abbreviateAccessModes(["ReadWriteOnce", "ReadOnlyMany", "Custom"]),
    ).toEqual(["RWO", "ROX", "Custom"]);
  });

  it("hides exact timestamps for zero dates", () => {
    expect(exactTimestamp("0001-01-01T00:00:00Z")).toBeUndefined();
    expect(exactTimestamp(undefined)).toBeUndefined();
    expect(exactTimestamp("2026-01-02T03:04:05Z")).toBeTruthy();
  });

  it("assigns layout kinds and plain search text", () => {
    const row = { n: "x", c: 3, p: ["80/TCP"], t: "2026-01-02T03:04:05Z" };
    const text = textColumn<typeof row>("n", "N", (r) => r.n);
    const count = countColumn<typeof row>("c", "C", (r) => r.c);
    const chips = chipColumn<typeof row>("p", "P", (r) => r.p);
    const age = ageColumn<typeof row>((r) => r.t);
    expect([text.kind, count.kind, chips.kind, age.kind]).toEqual([
      "text",
      "count",
      "badge",
      "age",
    ]);
    expect(chips.searchAccessor?.(row)).toBe("80/TCP");
    expect(count.searchAccessor?.(row)).toBe("3");
    expect(age.searchAccessor?.(row)).toMatch(/ago/);
  });
});
