import { describe, expect, it } from "vitest";
import { columnText, csvField, toCsv } from "@/components/ui/data-table-csv";

describe("csvField", () => {
  it("leaves plain text alone", () => {
    expect(csvField("nginx-abc")).toBe("nginx-abc");
  });

  it("quotes commas, quotes and newlines and doubles embedded quotes", () => {
    expect(csvField("a,b")).toBe('"a,b"');
    expect(csvField('say "hi"')).toBe('"say ""hi"""');
    expect(csvField("line1\nline2")).toBe('"line1\nline2"');
    expect(csvField("a\r\nb")).toBe('"a\r\nb"');
  });

  it("prefixes formula-injection leads with an apostrophe", () => {
    for (const lead of ["=SUM(A1)", "+1+1", "-cmd", "@evil", "\tx", "\rx"]) {
      expect(csvField(lead).replace(/^"/, "").startsWith("'")).toBe(true);
    }
    expect(csvField('=HYPERLINK("http://x","y")')).toBe(
      '"\'=HYPERLINK(""http://x"",""y"")"',
    );
  });

  it("does not mangle plain negative or signed numbers", () => {
    expect(csvField("-5")).toBe("-5");
    expect(csvField("+3.5")).toBe("+3.5");
    expect(csvField("1e-3")).toBe("1e-3");
  });
});

describe("toCsv", () => {
  type Row = { name: string; restarts: number; note?: string };
  const columns = [
    { header: "Name", accessor: (r: Row) => r.name },
    {
      header: "Restarts, total",
      accessor: (r: Row) => String(r.restarts),
      sortAccessor: (r: Row) => r.restarts,
    },
    {
      header: "Note",
      accessor: () => null,
      searchAccessor: (r: Row) => r.note ?? "",
    },
  ];

  it("emits CRLF rows with a header, escaped fields and search text", () => {
    const csv = toCsv(columns, [
      { name: "a", restarts: 3, note: 'x,"y"' },
      { name: "=b", restarts: -1 },
    ]);
    expect(csv).toBe(
      'Name,"Restarts, total",Note\r\na,3,"x,""y"""\r\n\'=b,-1,\r\n',
    );
  });

  it("yields only the header for no rows", () => {
    expect(toCsv(columns, [])).toBe('Name,"Restarts, total",Note\r\n');
  });
});

describe("columnText", () => {
  it("drops JSX/objects and booleans rather than printing [object Object]", () => {
    const jsx = { accessor: () => ({ type: "span" }), header: "x" };
    expect(columnText(jsx, 1)).toBe("");
    expect(columnText({ header: "x", accessor: () => true }, 1)).toBe("");
  });
});
