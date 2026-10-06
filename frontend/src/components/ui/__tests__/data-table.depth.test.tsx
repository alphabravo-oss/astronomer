import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { DataTable, type Column } from "@/components/ui/data-table";
import { useAuthStore } from "@/lib/store";

/** Minimal user-event stand-in (the package is not a dependency). */
const keyTokens = (text: string) =>
  text
    .match(/\{[A-Za-z]+\}|[\s\S]/g)
    ?.map((t) => (t.length > 1 ? t.slice(1, -1) : t)) ?? [];
const userEvent = {
  setup() {
    const keyboard = async (text: string) => {
      for (const key of keyTokens(text)) {
        const el = (document.activeElement ?? document.body) as HTMLElement;
        const notPrevented = fireEvent.keyDown(el, { key });
        if (
          notPrevented &&
          key.length === 1 &&
          (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement)
        ) {
          fireEvent.change(el, { target: { value: el.value + key } });
        }
        fireEvent.keyUp(el, { key });
        if (key === "Enter" && el instanceof HTMLButtonElement) el.click();
      }
    };
    return {
      click: async (el: Element) => {
        if (el instanceof HTMLElement) el.focus();
        fireEvent.click(el);
      },
      hover: async (el: Element) => {
        fireEvent.pointerEnter(el);
        fireEvent.pointerMove(el);
      },
      clear: async (el: Element) => {
        fireEvent.change(el, { target: { value: "" } });
      },
      type: async (el: Element, text: string) => {
        if (el instanceof HTMLElement) el.focus();
        await keyboard(text);
      },
      keyboard,
    };
  },
};

const downloadCsv = vi.fn();
vi.mock("@/components/ui/data-table-csv", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/components/ui/data-table-csv")>()),
  downloadCsv: (name: string, csv: string) => downloadCsv(name, csv),
}));

const api = vi.hoisted(() => ({
  listTableViews: vi.fn(),
  createTableView: vi.fn(),
  updateTableView: vi.fn(),
  deleteTableView: vi.fn(),
}));
vi.mock("@/lib/api/table-views", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/table-views")>()),
  listTableViews: (...a: unknown[]) => api.listTableViews(...a),
  createTableView: (...a: unknown[]) => api.createTableView(...a),
  updateTableView: (...a: unknown[]) => api.updateTableView(...a),
  deleteTableView: (...a: unknown[]) => api.deleteTableView(...a),
}));

/** jsdom has no layout: give the virtualizer a viewport (see data-table.behavior). */
function stubVirtualLayout() {
  const g = globalThis as Record<string, unknown>;
  const prevRO = g.ResizeObserver;
  g.ResizeObserver = class {
    cb: ResizeObserverCallback;
    constructor(cb: ResizeObserverCallback) {
      this.cb = cb;
    }
    observe(el: Element) {
      this.cb(
        [{ target: el } as ResizeObserverEntry],
        this as unknown as ResizeObserver,
      );
    }
    unobserve() {}
    disconnect() {}
  };
  const h = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "offsetHeight",
  );
  const w = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "offsetWidth",
  );
  Object.defineProperty(HTMLElement.prototype, "offsetHeight", {
    configurable: true,
    get() {
      return this.getAttribute("role") === "grid" ? 400 : 52;
    },
  });
  Object.defineProperty(HTMLElement.prototype, "offsetWidth", {
    configurable: true,
    get: () => 800,
  });
  return () => {
    if (h) Object.defineProperty(HTMLElement.prototype, "offsetHeight", h);
    if (w) Object.defineProperty(HTMLElement.prototype, "offsetWidth", w);
    g.ResizeObserver = prevRO;
  };
}

type Row = {
  id: string;
  name: string;
  status: string;
  restarts: number;
  uid: string;
};

const rows: Row[] = [
  {
    id: "b",
    name: "banana",
    status: "Running",
    restarts: 3,
    uid: "uid-b-1234567890abcdef",
  },
  {
    id: "a",
    name: "apple",
    status: "Failed",
    restarts: 0,
    uid: "uid-a-1234567890abcdef",
  },
  {
    id: "c",
    name: "=cherry",
    status: "Running",
    restarts: 7,
    uid: "uid-c-1234567890abcdef",
  },
];

const columns: Column<Row>[] = [
  { key: "name", header: "Name", kind: "name", accessor: (r) => r.name },
  {
    key: "status",
    header: "Status",
    kind: "status",
    accessor: (r) => r.status,
    sortAccessor: (r) => r.status,
    filter: { label: "State" },
  },
  {
    key: "restarts",
    header: "Restarts",
    kind: "count",
    accessor: (r) => r.restarts,
    sortAccessor: (r) => r.restarts,
  },
  { key: "uid", header: "UID", kind: "id", accessor: (r) => r.uid },
  {
    key: "actions",
    header: "",
    kind: "actions",
    rowActions: true,
    accessor: () => "...",
  },
];

const headerNames = () =>
  screen
    .getAllByRole("columnheader")
    .map((h) => h.textContent?.trim() ?? "")
    .filter(Boolean);

const renderTable = (
  props: Partial<Parameters<typeof DataTable<Row>>[0]> = {},
) =>
  render(
    <DataTable
      data={rows}
      columns={columns}
      keyExtractor={(r) => r.id}
      {...props}
    />,
  );

beforeEach(() => {
  downloadCsv.mockClear();
  window.localStorage.clear();
  window.history.replaceState({}, "", "/");
});

describe("column kinds", () => {
  it("sizes kind columns and right-aligns numeric ones", () => {
    renderTable();
    const status = screen.getByRole("columnheader", { name: /Status/ });
    expect(status).toHaveStyle({ width: "120px" });
    const restarts = screen.getByRole("columnheader", { name: /Restarts/ });
    expect(restarts).toHaveClass("text-right");
    // The grow column carries no fixed width so it absorbs the remainder.
    const name = screen.getByRole("columnheader", { name: /Name/ });
    expect(name.style.width).toBe("");
    expect(screen.getByText("7")).toHaveClass("tabular-nums");
  });

  it("never clips or wraps header labels of sized columns", () => {
    renderTable();
    const label = screen.getByText("Restarts");
    expect(label).toHaveClass("whitespace-nowrap");
    expect(label).not.toHaveClass("truncate");
  });

  it("shows the full value of a clipped name cell in a Tooltip, with no title attribute", async () => {
    const user = userEvent.setup();
    const original = Object.getOwnPropertyDescriptor(
      HTMLElement.prototype,
      "scrollWidth",
    );
    Object.defineProperty(HTMLElement.prototype, "scrollWidth", {
      configurable: true,
      get: () => 500,
    });
    try {
      renderTable();
      const cell = screen.getByText("banana");
      expect(cell.closest("[title]")).toBeNull();
      await user.hover(cell);
      // Radix opens on focus without the hover delay.
      fireEvent.focus(cell);
      const tip = await screen.findAllByText("banana");
      expect(tip.length).toBeGreaterThan(1);
    } finally {
      if (original)
        Object.defineProperty(HTMLElement.prototype, "scrollWidth", original);
    }
  });

  it("renders id columns as mono middle-ellipsis copy buttons", () => {
    renderTable();
    const button = screen.getByRole("button", {
      name: /Copy UID uid-b-1234567890abcdef/,
    });
    expect(button).toHaveClass("font-mono");
  });
});

describe("column order and pinning", () => {
  it("reorders from the Columns menu with keyboard-accessible buttons", async () => {
    const user = userEvent.setup();
    renderTable({ persistKey: "order-test" });
    expect(headerNames().slice(0, 3)).toEqual(["Name", "Status", "Restarts"]);
    await user.click(screen.getByRole("button", { name: "Columns" }));
    await user.click(screen.getByRole("button", { name: "Move Restarts up" }));
    expect(headerNames().slice(0, 3)).toEqual(["Name", "Restarts", "Status"]);
    // Persisted for the next mount.
    expect(window.localStorage.getItem("dt:order-test:order")).toContain(
      "restarts",
    );
  });

  it("pins a column to the start and keeps actions last", async () => {
    const user = userEvent.setup();
    renderTable();
    await user.click(screen.getByRole("button", { name: "Columns" }));
    await user.click(screen.getByRole("button", { name: "Pin UID left" }));
    expect(headerNames()[0]).toBe("UID");
    expect(screen.getAllByRole("columnheader").at(-1)).toHaveAccessibleName(
      "Actions",
    );
    const uid = screen.getByRole("columnheader", { name: /UID/ });
    expect(uid.style.position).toBe("sticky");
    // Unpin restores declared order.
    await user.click(screen.getByRole("button", { name: "Pin UID left" }));
    expect(headerNames()[0]).toBe("Name");
  });
});

describe("filter chips", () => {
  it("renders removable chips for facet filters and Clear all", async () => {
    const user = userEvent.setup();
    renderTable();
    await user.click(screen.getByRole("button", { name: /State/ }));
    await user.click(screen.getByRole("checkbox", { name: "Running" }));
    await user.click(screen.getByRole("checkbox", { name: "Failed" }));
    const group = screen.getByRole("group", { name: "Active filters" });
    expect(within(group).getByText("Running")).toBeInTheDocument();
    expect(within(group).getByText("Failed")).toBeInTheDocument();

    await user.click(
      within(group).getByRole("button", {
        name: "Remove filter State: Failed",
      }),
    );
    expect(screen.queryByText("apple")).not.toBeInTheDocument();
    expect(screen.getByText("banana")).toBeInTheDocument();

    await user.click(within(group).getByRole("button", { name: "Clear all" }));
    expect(screen.queryByRole("group", { name: "Active filters" })).toBeNull();
    expect(screen.getByText("apple")).toBeInTheDocument();
  });
});

describe("expandable rows", () => {
  it.each([false, true])(
    "toggles a sub row (virtualized=%s)",
    async (virtualized) => {
      const user = userEvent.setup();
      const restore = virtualized ? stubVirtualLayout() : () => {};
      renderTable({
        virtualized,
        renderSubRow: (row) => <div>Containers of {row.name}</div>,
      });
      const toggle = await screen.findByRole("button", {
        name: "Expand row b",
      });
      expect(toggle).toHaveAttribute("aria-expanded", "false");
      await user.click(toggle);
      expect(
        await screen.findByText("Containers of banana"),
      ).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Collapse row b" }),
      ).toHaveAttribute("aria-expanded", "true");
      await user.click(screen.getByRole("button", { name: "Collapse row b" }));
      expect(screen.queryByText("Containers of banana")).toBeNull();
      restore();
    },
  );
});

describe("keyboard row navigation", () => {
  it("moves focus with j/k and arrows, toggles selection with x, focuses search with /", async () => {
    const user = userEvent.setup();
    renderTable({ selectable: true, searchable: true });
    const region = screen.getByRole("region", { name: "Data table" });
    region.focus();
    await user.keyboard("j");
    const bodyRows = () =>
      screen
        .getAllByRole("row")
        .filter((r) => r.hasAttribute("data-row-index"));
    expect(bodyRows()[0]).toHaveFocus();
    await user.keyboard("j");
    expect(bodyRows()[1]).toHaveFocus();
    await user.keyboard("k");
    expect(bodyRows()[0]).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(bodyRows()[1]).toHaveFocus();

    await user.keyboard("x");
    expect(within(bodyRows()[1]).getByRole("checkbox")).toBeChecked();
    await user.keyboard("x");
    expect(within(bodyRows()[1]).getByRole("checkbox")).not.toBeChecked();

    await user.keyboard("/");
    expect(screen.getByPlaceholderText("Search...")).toHaveFocus();
  });

  it("is inert while a text input has focus", async () => {
    const user = userEvent.setup();
    renderTable();
    const search = screen.getByPlaceholderText("Search...");
    await user.click(search);
    await user.keyboard("jk/x");
    expect(search).toHaveValue("jk/x");
  });

  it("opens the focused row with Enter", async () => {
    const user = userEvent.setup();
    const onRowClick = vi.fn();
    renderTable({ onRowClick });
    const first = screen
      .getAllByRole("row")
      .find((r) => r.hasAttribute("data-row-index")) as HTMLElement;
    first.focus();
    await user.keyboard("{Enter}");
    expect(onRowClick).toHaveBeenCalledWith(rows[0]);
  });
});

describe("CSV export", () => {
  it("exports the filtered rows and visible columns with safe escaping", async () => {
    const user = userEvent.setup();
    renderTable({ exportCsv: true });
    await user.type(screen.getByPlaceholderText("Search..."), "an");
    await waitFor(() => expect(screen.queryByText("apple")).toBeNull());
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(downloadCsv).toHaveBeenCalledTimes(1);
    const [name, csv] = downloadCsv.mock.calls[0] as [string, string];
    expect(name).toMatch(/^table-\d{4}-\d{2}-\d{2}\.csv$/);
    const lines = csv.trim().split("\r\n");
    expect(lines[0]).toBe("Name,Status,Restarts,UID");
    expect(lines).toHaveLength(2);
    expect(lines[1]).toBe("banana,Running,3,uid-b-1234567890abcdef");
  });

  it("prefixes formula leads", async () => {
    const user = userEvent.setup();
    renderTable({ exportCsv: true });
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    const csv = (downloadCsv.mock.calls[0] as [string, string])[1];
    expect(csv).toContain("'=cherry,");
  });

  it("labels export as page-only on server-paged tables", () => {
    renderTable({
      exportCsv: true,
      serverSide: {
        rowCount: 90,
        pagination: { pageIndex: 0, pageSize: 3 },
        onPaginationChange: () => {},
      },
    });
    expect(
      screen.getByRole("button", { name: "Export page (CSV)" }),
    ).toBeInTheDocument();
  });
});

describe("server-side filtering", () => {
  it("delegates facet filters to the caller (manualFiltering)", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderTable({
      serverSide: {
        rowCount: 3,
        pagination: { pageIndex: 0, pageSize: 20 },
        onPaginationChange: () => {},
        filtering: { value: [], onChange },
      },
    });
    await user.click(screen.getByRole("button", { name: /State/ }));
    await user.click(screen.getByRole("checkbox", { name: "Failed" }));
    expect(onChange).toHaveBeenCalledWith([
      { id: "status", value: ["Failed"] },
    ]);
    // Rows are not filtered a second time on the client.
    expect(screen.getByText("banana")).toBeInTheDocument();
  });
});

describe("row density", () => {
  it("consumes --row-py with a density fallback", () => {
    renderTable();
    const cell = screen.getByText("banana").closest("td");
    expect(cell?.className).toMatch(/py-\[var\(--row-py,0\.75rem\)\]/);
  });

  it("an explicit density prop pins the padding", () => {
    renderTable({ density: "compact" });
    expect(screen.getByText("banana").closest("td")).toHaveClass("py-2");
  });
});

describe("loading skeleton", () => {
  it("renders Skeleton placeholders while loading", () => {
    const { container } = renderTable({ loading: true, loadingRows: 2 });
    expect(container.querySelectorAll("tbody tr")).toHaveLength(2);
    expect(
      container.querySelector("tbody [aria-hidden='true']"),
    ).not.toBeNull();
  });
});

describe("view state through the URL", () => {
  it("restores search and sort from the URL on mount", async () => {
    window.history.replaceState(
      {},
      "",
      `/?tv-url-test=${encodeURIComponent(
        JSON.stringify({
          search: "ban",
          sort: [{ id: "restarts", desc: true }],
        }),
      )}`,
    );
    renderTable({ persistKey: "url-test" });
    expect(screen.getByPlaceholderText("Search...")).toHaveValue("ban");
    await waitFor(() => expect(screen.queryByText("apple")).toBeNull());
  });

  function renderInRouter(entry: string, client: QueryClient) {
    const root = createRootRoute({ component: Outlet });
    const index = createRoute({
      getParentRoute: () => root,
      path: "/",
      validateSearch: (s: Record<string, unknown>) => s,
      component: () => (
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(r) => r.id}
          persistKey="rt"
        />
      ),
    });
    const router = createRouter({
      routeTree: root.addChildren([index]),
      history: createMemoryHistory({ initialEntries: [entry] }),
    });
    render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
    return router;
  }

  it("round-trips a view: state is written to the URL and re-applied on a fresh mount", async () => {
    const client = new QueryClient();
    const router = renderInRouter("/?keep=1", client);
    const user = userEvent.setup();
    const search = await screen.findByPlaceholderText("Search...");
    await user.type(search, "apple");
    await waitFor(
      () => {
        const params = new URLSearchParams(router.state.location.searchStr);
        expect(params.get("keep")).toBe("1");
        expect(JSON.parse(params.get("tv-rt") ?? "{}")).toMatchObject({
          search: "apple",
        });
      },
      { timeout: 2000 },
    );
    const written = router.state.location.href;
    act(() => {});
    document.body.innerHTML = "";

    renderInRouter(written, client);
    expect(await screen.findByPlaceholderText("Search...")).toHaveValue(
      "apple",
    );
    await waitFor(() => expect(screen.queryByText("banana")).toBeNull());
  });
});

describe("saved views menu", () => {
  const stored = {
    id: "v1",
    table_key: "views-test",
    name: "Failing",
    state: { v: 1, filters: { status: ["Failed"] } },
    is_default: false,
    created_at: "",
    updated_at: "",
  };

  function renderWithClient() {
    useAuthStore.setState({
      isAuthenticated: true,
      user: { id: "u1" } as never,
    });
    return render(
      <QueryClientProvider client={new QueryClient()}>
        <DataTable
          data={rows}
          columns={columns}
          keyExtractor={(r) => r.id}
          persistKey="views-test"
        />
      </QueryClientProvider>,
    );
  }

  afterEach(() => {
    useAuthStore.setState({ isAuthenticated: false, user: null });
    vi.clearAllMocks();
  });

  it("applies a saved view", async () => {
    api.listTableViews.mockResolvedValue([stored]);
    const user = userEvent.setup();
    renderWithClient();
    await user.click(await screen.findByRole("button", { name: /Views/ }));
    await user.click(await screen.findByRole("button", { name: "Failing" }));
    await waitFor(() => expect(screen.queryByText("banana")).toBeNull());
    expect(screen.getByText("apple")).toBeInTheDocument();
  });

  it("applies the default view on load", async () => {
    api.listTableViews.mockResolvedValue([{ ...stored, is_default: true }]);
    renderWithClient();
    await waitFor(() => expect(screen.queryByText("banana")).toBeNull());
  });

  it("saves the current state under a name", async () => {
    api.listTableViews.mockResolvedValue([]);
    api.createTableView.mockResolvedValue(stored);
    const user = userEvent.setup();
    renderWithClient();
    await user.type(screen.getByPlaceholderText("Search..."), "apple");
    await user.click(await screen.findByRole("button", { name: /Views/ }));
    await user.type(screen.getByLabelText("View name"), "My apples");
    await user.click(screen.getByRole("button", { name: "Save current view" }));
    await waitFor(() => expect(api.createTableView).toHaveBeenCalled());
    expect(api.createTableView).toHaveBeenCalledWith({
      tableKey: "views-test",
      name: "My apples",
      state: expect.objectContaining({ search: "apple" }),
    });
  });

  it("renames, sets default and deletes", async () => {
    api.listTableViews.mockResolvedValue([stored]);
    api.updateTableView.mockResolvedValue(stored);
    api.deleteTableView.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderWithClient();
    await user.click(await screen.findByRole("button", { name: /Views/ }));
    await user.click(
      await screen.findByRole("button", { name: "Rename Failing" }),
    );
    const input = screen.getByLabelText("New name for view Failing");
    await user.clear(input);
    await user.type(input, "Broken{Enter}");
    await waitFor(() =>
      expect(api.updateTableView).toHaveBeenCalledWith("v1", {
        name: "Broken",
      }),
    );
    await user.click(
      screen.getByRole("button", { name: "Set Failing as default" }),
    );
    await waitFor(() =>
      expect(api.updateTableView).toHaveBeenCalledWith("v1", {
        is_default: true,
      }),
    );
    await user.click(screen.getByRole("button", { name: "Delete Failing" }));
    await user.click(
      screen.getByRole("button", { name: "Confirm delete Failing" }),
    );
    await waitFor(() => expect(api.deleteTableView).toHaveBeenCalledWith("v1"));
  });

  it("hides the menu without a query client or signed-in user", () => {
    renderTable({ persistKey: "views-test" });
    expect(screen.queryByRole("button", { name: /Views/ })).toBeNull();
    void fireEvent;
  });
});
