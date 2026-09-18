import { act, renderHook } from "@testing-library/react";
import { useTable, type TableOptions } from "@tanstack/react-table";
import { describe, expect, it } from "vitest";
import {
  dataTableFeatures,
  type DataTableFeatures,
} from "../data-table-features";

interface Item {
  id: string;
  size: number;
}
const options: TableOptions<DataTableFeatures, Item> = {
  features: dataTableFeatures,
  data: [
    { id: "a", size: 30 },
    { id: "b", size: 10 },
    { id: "c", size: 20 },
  ],
  columns: [
    {
      accessorKey: "size",
      sortFn: (a, b) => a.original.size - b.original.size,
    },
  ],
  getRowId: (row) => row.id,
  initialState: { pagination: { pageIndex: 0, pageSize: 2 } },
  autoResetPageIndex: false,
};

describe("DataTable feature graph", () => {
  it("reactively sorts, pages, selects and hides columns through the native controller", () => {
    const { result } = renderHook(() => useTable(options));
    expect(result.current.getRowModel().rows.map((row) => row.id)).toEqual([
      "a",
      "b",
    ]);
    act(() => result.current.setSorting([{ id: "size", desc: false }]));
    expect(result.current.getRowModel().rows.map((row) => row.id)).toEqual([
      "b",
      "c",
    ]);
    act(() => result.current.nextPage());
    expect(result.current.state.pagination.pageIndex).toBe(1);
    expect(result.current.getRowModel().rows.map((row) => row.id)).toEqual([
      "a",
    ]);
    act(() => result.current.getRow("a").toggleSelected());
    expect(
      result.current.getSelectedRowModel().rows.map((row) => row.id),
    ).toEqual(["a"]);
    act(() => result.current.getColumn("size")?.toggleVisibility(false));
    expect(result.current.getVisibleLeafColumns()).toEqual([]);
  });

  it("bypasses pagination for virtualization without bypassing sorting", () => {
    const { result } = renderHook(() =>
      useTable({ ...options, manualPagination: true }),
    );
    act(() => result.current.setSorting([{ id: "size", desc: true }]));
    expect(result.current.getRowModel().rows.map((row) => row.id)).toEqual([
      "a",
      "c",
      "b",
    ]);
  });
});
