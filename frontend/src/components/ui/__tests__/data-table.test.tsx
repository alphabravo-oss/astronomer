import { fireEvent, render, screen } from "@testing-library/react";
import { DataTable, type Column } from "@/components/ui/data-table";

type Row = {
  id: string;
  name: string;
  status: string;
};

const rows: Row[] = [
  { id: "one", name: "One", status: "ready" },
  { id: "two", name: "Two", status: "pending" },
];

const columns: Column<Row>[] = [
  {
    key: "name",
    header: "Name",
    accessor: (row) => row.name,
  },
  {
    key: "status",
    header: "Status",
    accessor: (row) => row.status,
  },
];

describe("DataTable", () => {
  it("resizes a newly supplied column and persists its native sizing state", () => {
    window.localStorage.clear();
    const { rerender } = render(
      <DataTable
        data={rows}
        columns={[columns[0]]}
        keyExtractor={(row) => row.id}
        resizable
        persistKey="resize-test"
      />,
    );
    rerender(
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(row) => row.id}
        resizable
        persistKey="resize-test"
      />,
    );
    const handle = screen.getByRole("button", { name: /Resize Status column/ });
    fireEvent.keyDown(handle, { key: "ArrowRight" });
    expect(handle).toHaveAccessibleName(/currently 166 pixels/);
    expect(
      JSON.parse(window.localStorage.getItem("dt:resize-test:sizing")!),
    ).toMatchObject({ status: 166 });
    window.localStorage.clear();
  });

  it("renders bulk actions with selected rows", () => {
    render(
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(row) => row.id}
        selectable
        bulkActions={(selected) => <button>Delete {selected.length}</button>}
      />,
    );

    const [, firstRowCheckbox] = screen.getAllByRole("checkbox");
    fireEvent.click(firstRowCheckbox);

    expect(screen.getByText("1 row selected")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Delete 1" }),
    ).toBeInTheDocument();
  });

  it("fails closed for rows excluded by the selection predicate", () => {
    render(
      <DataTable
        data={rows}
        columns={columns}
        keyExtractor={(row) => row.id}
        selectable={(row) => row.status === "ready"}
        bulkActions={(selected) => <button>Delete {selected.length}</button>}
      />,
    );

    const [, ready, pending] = screen.getAllByRole("checkbox");
    expect(ready).toBeEnabled();
    expect(pending).toBeDisabled();
    fireEvent.click(pending);
    expect(screen.queryByText(/row selected/)).not.toBeInTheDocument();
  });

  it("reserves the requested number of loading rows", () => {
    render(
      <DataTable
        data={[]}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading
        loadingRows={3}
        density="compact"
      />,
    );

    expect(screen.getByRole("columnheader", { name: /name/i })).toHaveClass(
      "py-2",
    );
    expect(screen.getAllByRole("row")).toHaveLength(4);
  });

  it.each([
    [401, "Permission required"],
    [403, "Permission required"],
    [0, "Connection unavailable"],
    [500, "Failed to load — try again"],
  ])("classifies query failure status %s", (status, expected) => {
    render(
      <DataTable
        data={[]}
        columns={columns}
        keyExtractor={(row) => row.id}
        isError
        error={{ status }}
        permission="clusters:read"
      />,
    );

    expect(screen.getByText(expected)).toBeInTheDocument();
    expect(screen.queryByText("No records yet")).not.toBeInTheDocument();
  });
});
