import { act, fireEvent, render, screen } from "@testing-library/react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ClippedValue } from "@/components/ui/data-table-cell";

const downloadCsv = vi.fn();
vi.mock("@/components/ui/data-table-csv", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/components/ui/data-table-csv")>()),
  downloadCsv: (name: string, csv: string) => downloadCsv(name, csv),
}));

type Row = { id: string; name: string; cpu: number };
const rows: Row[] = [{ id: "a", name: "alpha", cpu: 3 }];
const columns: Column<Row>[] = [
  { key: "name", header: "Name", accessor: (r) => r.name },
  {
    key: "cpu",
    header: "CPU",
    ariaLabel: "CPU requests (cores)",
    sortAccessor: (r) => r.cpu,
    accessor: (r) => r.cpu,
  },
  {
    key: "plain",
    header: "Plain",
    ariaLabel: "Plain full name",
    headerTooltip: "Custom tip",
    sortable: false,
    accessor: () => "x",
  },
];

const renderTable = () =>
  render(
    <DataTable
      data={rows}
      columns={columns}
      keyExtractor={(r) => r.id}
      exportCsv
    />,
  );

describe("column ariaLabel / headerTooltip", () => {
  it("keeps short visible text and exposes the full name", () => {
    renderTable();
    expect(
      screen.getByRole("columnheader", { name: "CPU requests (cores)" }),
    ).toBeInTheDocument();
    expect(screen.getByText("CPU")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sort by CPU requests (cores)" }),
    ).toBeInTheDocument();
    // The columnheader itself carries the full name, sortable or not.
    expect(
      screen.getByRole("columnheader", { name: "Plain full name" }),
    ).toBeInTheDocument();
  });

  it("leaves columns without the new props unchanged", () => {
    renderTable();
    expect(
      screen.getByRole("columnheader", { name: /Name/ }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sort by Name" }),
    ).toBeInTheDocument();
  });

  it("shows a tooltip on focus (headerTooltip wins over ariaLabel)", async () => {
    renderTable();
    await act(async () => {
      fireEvent.focus(
        screen.getByRole("button", { name: "Sort by CPU requests (cores)" }),
      );
    });
    expect(
      await screen.findByRole("tooltip", { name: "CPU requests (cores)" }),
    ).toBeInTheDocument();
  });

  it("uses the full name in the Columns menu", () => {
    renderTable();
    fireEvent.click(screen.getByRole("button", { name: "Columns" }));
    expect(
      screen.getByRole("checkbox", { name: "CPU requests (cores)" }),
    ).toBeInTheDocument();
  });

  it("exports the full name as the CSV header", () => {
    downloadCsv.mockClear();
    renderTable();
    fireEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    const csv = (downloadCsv.mock.calls[0] as [string, string])[1];
    expect(csv.split("\r\n")[0]).toBe(
      "Name,CPU requests (cores),Plain full name",
    );
  });
});

describe("ClippedValue", () => {
  const measure = (scrollWidth: number, clientWidth: number) => {
    render(<ClippedValue>long value</ClippedValue>);
    const el = screen.getByText("long value");
    Object.defineProperty(el, "scrollWidth", { value: scrollWidth });
    Object.defineProperty(el, "clientWidth", { value: clientWidth });
    return el;
  };

  it("shows the full value in a tooltip only when clipped", async () => {
    const el = measure(300, 100);
    expect(el).toHaveAttribute("data-cell-clip", "");
    await act(async () => {
      fireEvent.pointerEnter(el);
    });
    await act(async () => {
      fireEvent.focus(el);
    });
    expect(await screen.findByRole("tooltip")).toHaveTextContent("long value");
  });

  it("passes through without a tooltip when not clipped", async () => {
    const el = measure(100, 100);
    await act(async () => {
      fireEvent.pointerEnter(el);
      fireEvent.focus(el);
    });
    expect(screen.queryByRole("tooltip")).toBeNull();
  });

  it("supports a tooltip override and two-line clamping", async () => {
    render(
      <ClippedValue lines={2} tooltip="override">
        text
      </ClippedValue>,
    );
    const el = screen.getByText("text");
    expect(el).toHaveClass("line-clamp-2");
    Object.defineProperty(el, "scrollHeight", { value: 90 });
    Object.defineProperty(el, "clientHeight", { value: 40 });
    await act(async () => {
      fireEvent.pointerEnter(el);
      fireEvent.focus(el);
    });
    expect(await screen.findByRole("tooltip")).toHaveTextContent("override");
  });
});
