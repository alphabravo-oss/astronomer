import { describe, expect, it } from "vitest";
import { resolveColumnLayout } from "@/components/ui/data-table-layout";
import { auditColumns } from "../../audit/-columns";
import { searchColumns } from "../../search/-columns";
import { connectorColumns } from "./-columns";

const tables = {
  audit: auditColumns(new Map()),
  search: searchColumns("pods"),
  connectors: connectorColumns({ onEdit: () => {}, onDelete: () => {} }),
};

describe.each(Object.entries(tables))("%s columns", (_name, columns) => {
  it("declares a kind on every column", () => {
    expect(columns.filter((c) => !c.kind)).toEqual([]);
  });
  it("grows exactly one column", () => {
    const growers = columns.filter((c) => resolveColumnLayout(c).grow);
    expect(growers).toHaveLength(1);
  });
});
