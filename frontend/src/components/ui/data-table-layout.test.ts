import { describe, expect, it } from "vitest";
import {
  COLUMN_KINDS,
  computeColumnWidths,
  defaultPinning,
  flexCellStyle,
  headerFloor,
  moveColumn,
  orderColumns,
  pinnedPlacements,
  resolveColumnLayout,
  SELECT_COLUMN_WIDTH,
  tableCellStyle,
  type ColumnKind,
} from "@/components/ui/data-table-layout";

const col = (key: string, extra: Record<string, unknown> = {}) => ({
  key,
  header: key,
  ...extra,
});

describe("COLUMN_KINDS", () => {
  const kinds = Object.keys(COLUMN_KINDS) as ColumnKind[];

  it("defines every kind with min <= size <= max", () => {
    expect(kinds).toHaveLength(13);
    for (const kind of kinds) {
      const spec = COLUMN_KINDS[kind];
      expect(spec.minSize, kind).toBeLessThanOrEqual(spec.size);
      if (spec.maxSize !== undefined) {
        expect(spec.size, kind).toBeLessThanOrEqual(spec.maxSize);
      }
    }
  });

  it("right-aligns numeric kinds and gives only name a grow", () => {
    for (const kind of ["count", "percent", "bytes"] as const) {
      expect(COLUMN_KINDS[kind].align).toBe("right");
      expect(COLUMN_KINDS[kind].numeric).toBe(true);
    }
    expect(kinds.filter((k) => COLUMN_KINDS[k].grow)).toEqual(["name"]);
  });

  it("pins actions to the end and select to the start; id is mono middle", () => {
    expect(COLUMN_KINDS.actions.pin).toBe("end");
    expect(COLUMN_KINDS.select.pin).toBe("start");
    expect(COLUMN_KINDS.id).toMatchObject({ mono: true, overflow: "middle" });
  });

  it("never truncates the short kinds", () => {
    for (const kind of [
      "status",
      "badge",
      "count",
      "age",
      "version",
    ] as const) {
      expect(COLUMN_KINDS[kind].overflow).toBe("nowrap");
    }
    expect(COLUMN_KINDS.name.overflow).toBe("truncate");
    expect(COLUMN_KINDS.text.overflow).toBe("wrap-2");
  });
});

describe("resolveColumnLayout precedence", () => {
  it("uses the kind defaults", () => {
    const r = resolveColumnLayout(col("n", { kind: "name" }));
    expect(r).toMatchObject({ size: 240, grow: true, align: "left" });
  });

  it("explicit size/minSize/maxSize/align/grow override the kind", () => {
    const r = resolveColumnLayout(
      col("n", {
        kind: "count",
        size: 200,
        minSize: 150,
        maxSize: 300,
        align: "left",
        grow: true,
      }),
    );
    expect(r).toMatchObject({
      size: 200,
      minSize: 150,
      maxSize: 300,
      align: "left",
      grow: true,
    });
  });

  it("an explicit legacy width wins over the kind size and drops grow", () => {
    const r = resolveColumnLayout(col("n", { kind: "name", width: "10rem" }));
    expect(r.size).toBeUndefined();
    expect(r.cssWidth).toBe("10rem");
    expect(r.grow).toBe(false);
  });

  it("a column with no kind and no width keeps today's behavior", () => {
    const r = resolveColumnLayout(col("legacy"));
    expect(r).toMatchObject({
      sized: false,
      overflow: "legacy",
      grow: false,
      align: "left",
    });
    expect(tableCellStyle(r, {})).toEqual({});
    expect(flexCellStyle(r, { scroll: false })).toEqual({
      flex: "1 1 0",
      minWidth: 0,
    });
  });

  it("legacy rowActions keep the 2.5rem footprint and pin end", () => {
    const r = resolveColumnLayout(col("a", { rowActions: true, header: "" }));
    expect(r.cssWidth).toBe("2.5rem");
    expect(r.pin).toBe("end");
    expect(r.overflow).toBe("fixed");
  });

  it("wrap overrides the kind overflow", () => {
    expect(
      resolveColumnLayout(col("n", { kind: "name", wrap: true })).overflow,
    ).toBe("wrap");
  });
});

describe("header floor", () => {
  it("widens a short kind to fit the header label plus sort icon", () => {
    const r = resolveColumnLayout(
      col("restarts", { kind: "count", header: "Restart count" }),
    );
    expect(r.minSize).toBe(headerFloor({ header: "Restart count" }));
    expect(r.minSize).toBeGreaterThan(COLUMN_KINDS.count.minSize);
    expect((r.maxSize ?? 0) >= r.minSize).toBe(true);
    expect((r.size ?? 0) >= r.minSize).toBe(true);
  });

  it("is smaller for unsortable headers and zero for structural kinds", () => {
    expect(headerFloor({ header: "Name", sortable: false })).toBeLessThan(
      headerFloor({ header: "Name" }),
    );
    expect(headerFloor({ header: "Actions", kind: "actions" })).toBe(0);
  });
});

describe("computeColumnWidths (grow math)", () => {
  const layouts = [
    resolveColumnLayout(col("name", { kind: "name", header: "Name" })),
    resolveColumnLayout(col("status", { kind: "status", header: "State" })),
    resolveColumnLayout(col("age", { kind: "age", header: "Age" })),
  ];

  it("gives the grow column everything the fixed columns leave", () => {
    const widths = computeColumnWidths(layouts, 1000);
    expect(widths[1]).toBe(layouts[1].size);
    expect(widths[2]).toBe(layouts[2].size);
    expect(widths[0]).toBe(1000 - widths[1] - widths[2]);
  });

  it("never shrinks the grow column below its minimum", () => {
    const widths = computeColumnWidths(layouts, 100);
    expect(widths[0]).toBe(layouts[0].minSize);
  });

  it("shrinks fixed columns toward their minimums before squeezing the grow column", () => {
    const wide = [
      resolveColumnLayout(col("name", { grow: true, minSize: 200, size: 200 })),
      resolveColumnLayout(col("a", { size: 400, minSize: 120 })),
      resolveColumnLayout(col("b", { size: 400, minSize: 120 })),
    ];
    // Preferred 1000px of fixed columns + 200 grow minimum in a 800px box.
    const widths = computeColumnWidths(wide, 800);
    expect(widths[0]).toBeGreaterThanOrEqual(200);
    expect(widths[1]).toBeGreaterThanOrEqual(120);
    expect(widths[1]).toBeLessThan(400);
    expect(widths[0] + widths[1] + widths[2]).toBeLessThanOrEqual(800 + 0.001);
  });

  it("splits remaining width equally across unsized legacy columns", () => {
    const legacy = [col("a"), col("b")].map(resolveColumnLayout);
    expect(computeColumnWidths(legacy, 600)).toEqual([300, 300]);
  });

  it("respects maxSize on a grow column and redistributes", () => {
    const two = [
      resolveColumnLayout(col("a", { grow: true, size: 100, maxSize: 200 })),
      resolveColumnLayout(col("b", { grow: true, size: 100 })),
    ];
    const widths = computeColumnWidths(two, 1000);
    expect(widths).toEqual([200, 800]);
  });
});

describe("ordering and pinning", () => {
  const cols = ["sel", "name", "ns", "status", "age", "actions"].map((key) =>
    col(key),
  );
  const keys = (list: Array<{ key: string }>) => list.map((c) => c.key);

  it("places start-pinned first and end-pinned last, regardless of order", () => {
    const out = orderColumns(cols, ["age", "status", "ns", "name"], {
      start: ["name"],
      end: ["actions"],
    });
    expect(keys(out)).toEqual([
      "name",
      "age",
      "status",
      "ns",
      "sel",
      "actions",
    ]);
  });

  it("appends columns missing from order in declared order", () => {
    const out = orderColumns(cols, ["status"], { start: [], end: [] });
    expect(keys(out)).toEqual([
      "status",
      "sel",
      "name",
      "ns",
      "age",
      "actions",
    ]);
  });

  it("ignores unknown pinned ids and never duplicates a column", () => {
    const out = orderColumns(cols, [], { start: ["ghost", "ns"], end: ["ns"] });
    expect(keys(out)).toHaveLength(cols.length);
    expect(keys(out)[0]).toBe("ns");
  });

  it("default pinning: actions end; name start only for scroll tables", () => {
    const defs = [
      col("name", { kind: "name" }),
      col("age", { kind: "age" }),
      col("act", { kind: "actions", header: "" }),
    ];
    expect(defaultPinning(defs, "fit")).toEqual({ start: [], end: ["act"] });
    expect(defaultPinning(defs, "scroll")).toEqual({
      start: ["name"],
      end: ["act"],
    });
  });

  it("computes sticky offsets that stack widths and leave room for selection", () => {
    const defs = [
      col("name", { size: 200 }),
      col("ns", { size: 100 }),
      col("a", { kind: "actions", header: "" }),
      col("b", { size: 60 }),
    ];
    const placements = pinnedPlacements(
      defs,
      { start: ["name", "ns"], end: ["b", "a"] },
      SELECT_COLUMN_WIDTH,
    );
    expect(placements.get("name")).toMatchObject({ side: "start", offset: 40 });
    expect(placements.get("ns")).toMatchObject({
      offset: 240,
      edge: true,
    });
    expect(placements.get("name")?.edge).toBe(false);
    expect(placements.get("a")).toMatchObject({ side: "end", offset: 0 });
    expect(placements.get("b")).toMatchObject({ offset: 48, edge: true });
  });

  it("moveColumn swaps neighbours and stops at the ends", () => {
    expect(moveColumn(["a", "b", "c"], "b", -1)).toEqual(["b", "a", "c"]);
    expect(moveColumn(["a", "b", "c"], "a", -1)).toEqual(["a", "b", "c"]);
    expect(moveColumn(["a", "b", "c"], "c", 1)).toEqual(["a", "b", "c"]);
  });
});
