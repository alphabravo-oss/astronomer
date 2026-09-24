import { render, screen, fireEvent } from "@testing-library/react";
import { DataTable } from "../data-table";
import { pageTableCount } from "@/lib/api/pagination";

it("does not claim an offset is a total when a collection shrinks", () => {
  render(
    <DataTable
      data={[]}
      columns={[]}
      keyExtractor={() => ""}
      serverSide={{
        ...pageTableCount({
          data: [],
          pagination: {
            limit: 25,
            offset: 225,
            has_more: false,
            next_offset: null,
          },
        }),
        pagination: { pageIndex: 9, pageSize: 25 },
        onPaginationChange: vi.fn(),
      }}
    />,
  );
  expect(screen.getByText("Row count unavailable")).toBeVisible();
  expect(screen.getByRole("button", { name: "Previous page" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
});

it.each(["loading", "denied"])(
  "withholds counts on a %s later page but permits going back",
  (state) => {
    const change = vi.fn();
    render(
      <DataTable
        data={[]}
        columns={[]}
        keyExtractor={() => ""}
        loading={state === "loading"}
        isError={state === "denied"}
        error={{ status: 403 }}
        serverSide={{
          rowCount: 0,
          pagination: { pageIndex: 1, pageSize: 25 },
          onPaginationChange: change,
        }}
      />,
    );
    expect(screen.getByText("Row count unavailable")).toBeVisible();
    expect(screen.queryByText(/Showing/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
    expect(change).toHaveBeenCalledWith({ pageIndex: 0, pageSize: 25 });
  },
);

it.each([
  { total: undefined, size: 25 },
  { total: 200, size: 25 },
  { total: undefined, size: 1 },
])("labels totals truthfully (%j)", ({ total, size }) => {
  const data = Array.from({ length: size }, (_, id) => ({ id }));
  const change = vi.fn();
  render(
    <DataTable
      data={data}
      columns={[{ key: "id", header: "ID", accessor: (row) => row.id }]}
      keyExtractor={(row) => String(row.id)}
      serverSide={{
        ...pageTableCount({
          data,
          pagination: {
            total,
            limit: 25,
            offset: 0,
            has_more: true,
            next_offset: 25,
          },
        }),
        pagination: { pageIndex: 0, pageSize: 25 },
        onPaginationChange: change,
      }}
    />,
  );
  expect(
    screen.getByText(
      total
        ? "Showing 1-25 of 200"
        : `Showing 1-${size} of at least ${size + 1}`,
    ),
  ).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  expect(change).toHaveBeenCalledWith({ pageIndex: 1, pageSize: 25 });
});
it("keeps a way back from an empty later page", () => {
  const change = vi.fn();
  render(
    <DataTable
      data={[]}
      columns={[]}
      keyExtractor={() => ""}
      serverSide={{
        rowCount: 0,
        pagination: { pageIndex: 1, pageSize: 25 },
        onPaginationChange: change,
      }}
    />,
  );
  expect(screen.getByText("Showing 0-0 of 0")).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
  expect(change).toHaveBeenCalledWith({ pageIndex: 0, pageSize: 25 });
});
