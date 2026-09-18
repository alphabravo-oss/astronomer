import {
  buildSearchIndex,
  searchIndexMatches,
} from "@/components/ui/data-table-search";

type Row = { id: string; name: string; state: string };

describe("DataTable search index", () => {
  it("builds 5k searchable haystacks once, then filters without rerunning accessors", () => {
    const rows: Row[] = Array.from({ length: 5_000 }, (_, index) => ({
      id: String(index),
      name: `Resource ${index}`,
      state: index % 2 === 0 ? "ready" : "pending",
    }));
    const searchAccessor = vi.fn((row: Row) => row.name);
    const index = buildSearchIndex(
      rows,
      [
        {
          key: "name",
          accessor: (row: Row) => row.name,
          searchAccessor,
        },
        {
          key: "state",
          accessor: (row: Row) => row.state,
        },
      ],
      () => true,
      (row) => row.id,
    );

    expect(index).toHaveLength(5_000);
    expect(searchAccessor).toHaveBeenCalledTimes(5_000);

    expect(searchIndexMatches(index, "4999", "resource 4999")).toBe(true);
    expect(searchIndexMatches(index, "4999", "ready")).toBe(false);
    expect(searchIndexMatches(index, "4998", "ready")).toBe(true);
    expect(searchIndexMatches(index, "4998", "does not exist")).toBe(false);
    // Querying a precomputed index never re-invokes per-row accessors.
    expect(searchAccessor).toHaveBeenCalledTimes(5_000);
  });

  it("normalizes diacritics in both the indexed cell and query", () => {
    const index = buildSearchIndex(
      [{ id: "cafe", name: "Café" }],
      [{ key: "name", accessor: (row) => row.name }],
      () => true,
      (row) => row.id,
    );

    expect(searchIndexMatches(index, "cafe", "cafe")).toBe(true);
  });
});
