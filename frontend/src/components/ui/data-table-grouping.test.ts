import { describe, expect, it } from "vitest";
import { groupTableRows, tableGroupPositions } from "./data-table-grouping";
describe("table namespace grouping", () => {
  it("groups the current page only and preserves order within each namespace", () => {
    const rows = [
      { ns: "z", name: "z1" },
      { ns: "a", name: "a2" },
      { ns: "a", name: "a1" },
    ];
    expect(
      groupTableRows(rows, (row) => row.ns).map((row) => row.name),
    ).toEqual(["a2", "a1", "z1"]);
    expect(rows[0].name).toBe("z1");
  });
  it("accounts for group and column headers without making them selectable data rows", () => {
    expect(tableGroupPositions(["a", "a", "b"], (row) => row)).toEqual({
      rowCount: 6,
      positions: [
        { label: "a", start: true, rowIndex: 3 },
        { label: "a", start: false, rowIndex: 4 },
        { label: "b", start: true, rowIndex: 6 },
      ],
    });
    expect(tableGroupPositions([1, 2]).rowCount).toBe(3);
  });
});
