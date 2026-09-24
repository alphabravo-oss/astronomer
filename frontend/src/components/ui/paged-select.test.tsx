import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PagedSelect, type SelectPage } from "./paged-select";

type Item = { id: string; name: string; ready: boolean };
const pickerRoot = ["picker"] as const;
const items = Array.from({ length: 226 }, (_, i) => ({
  id: `id-${i + 1}`,
  name: `Item ${i + 1}`,
  ready: true,
}));
function page(offset: number, count = 25) {
  return {
    data: items.slice(offset, offset + count),
    pagination: {
      limit: count,
      offset,
      total: 226,
      has_more: offset + count < 226,
      next_offset: offset + count < 226 ? offset + count : null,
    },
  };
}
const fetchPage = vi.fn(async ({ offset }: SelectPage, _signal: AbortSignal) =>
  page(offset),
);
function mount(
  extra: Partial<React.ComponentProps<typeof PagedSelect<Item>>> = {},
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ scope = "one" }: { scope?: string } = {}) => (
    <QueryClientProvider client={client}>
      <form>
        <PagedSelect
          key={scope}
          label="Bundle"
          permission="delivery_bundles:list"
          name="bundle"
          queryKey={(params) => ["picker", scope, params]}
          fetchPage={fetchPage}
          optionLabel={(row) => row.name}
          required
          {...extra}
        />
      </form>
    </QueryClientProvider>
  );
  const view = render(wrapper());
  return {
    client,
    updateScope: (scope: string) => view.rerender(wrapper({ scope })),
  };
}
beforeEach(() => {
  fetchPage.mockReset();
  fetchPage.mockImplementation(async ({ offset }) => page(offset));
});
it("reaches option 226 through bounded pages and retains selected IDs between pages", async () => {
  mount();
  const select = screen.getByRole("combobox") as HTMLSelectElement;
  await screen.findByRole("option", { name: "Item 1" });
  fireEvent.change(select, { target: { value: "id-1" } });
  for (let i = 1; i <= 9; i++) {
    fireEvent.click(screen.getByRole("button", { name: "Next bundle page" }));
    await screen.findByRole("option", {
      name: `Item ${i * 25 + 1}`,
    });
    expect(select.value).toBe("id-1");
  }
  fireEvent.change(select, { target: { value: "id-226" } });
  expect(new FormData(select.form!).get("bundle")).toBe("id-226");
  expect(
    fetchPage.mock.calls.every(
      ([params, signal]) =>
        params.limit === 25 && signal instanceof AbortSignal,
    ),
  ).toBe(true);
});
it("uses the actual continuation offset even after a short page", async () => {
  fetchPage.mockImplementation(async ({ offset }) =>
    page(offset, offset === 0 ? 7 : 25),
  );
  mount();
  await screen.findByRole("option", { name: "Item 1" });
  fireEvent.click(screen.getByRole("button", { name: "Next bundle page" }));
  await screen.findByRole("option", { name: "Item 8" });
  expect(fetchPage).toHaveBeenLastCalledWith(
    { limit: 25, offset: 7 },
    expect.any(AbortSignal),
  );
});
it("keeps visible-page navigation usable during a background refresh", async () => {
  const { client } = mount();
  await screen.findByRole("option", { name: "Item 1" });
  let finish!: (value: ReturnType<typeof page>) => void;
  fetchPage.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  act(() => {
    void client.invalidateQueries({ queryKey: pickerRoot });
  });
  await waitFor(() => expect(client.isFetching()).toBe(1));
  const next = screen.getByRole("button", { name: "Next bundle page" });
  try {
    expect(next).toBeEnabled();
    fireEvent.click(next);
    await screen.findByRole("option", { name: "Item 26" });
  } finally {
    await act(async () => {
      finish(page(0));
    });
  }
});
it("keeps Next available when every option on the current page is ineligible", async () => {
  mount({ eligible: (row) => Number(row.id.slice(3)) > 25 });
  await screen.findByText("No eligible options on this page.");
  fireEvent.click(screen.getByRole("button", { name: "Next bundle page" }));
  expect(
    await screen.findByRole("option", { name: "Item 26" }),
  ).toBeInTheDocument();
});
it.each([403, 500, 0])(
  "blocks submission and hides cached labels after %s; Previous recovers",
  async (status) => {
    const { client } = mount();
    await screen.findByRole("option", { name: "Item 1" });
    fireEvent.click(screen.getByRole("button", { name: "Next bundle page" }));
    await screen.findByRole("option", { name: "Item 26" });
    const select = screen.getByRole("combobox") as HTMLSelectElement;
    fireEvent.change(select, { target: { value: "id-26" } });
    fetchPage.mockImplementation(async ({ offset }) => {
      if (offset) throw { status };
      return page(0);
    });
    await act(async () => {
      await client.invalidateQueries({ queryKey: pickerRoot });
    });
    await waitFor(() => expect(select.validity.customError).toBe(true));
    expect(
      screen.queryByRole("option", { name: "Item 26" }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Previous bundle page" }),
    );
    await screen.findByRole("option", { name: "Item 1" });
    await waitFor(() => expect(select.checkValidity()).toBe(true));
  },
);
it("invalidates a selected version that becomes ineligible", async () => {
  const { client } = mount({ eligible: (row) => row.ready });
  await screen.findByRole("option", { name: "Item 1" });
  const select = screen.getByRole("combobox") as HTMLSelectElement;
  fireEvent.change(select, { target: { value: "id-1" } });
  fetchPage.mockResolvedValue({
    ...page(0),
    data: [{ ...items[0], ready: false }],
  });
  await act(async () => {
    await client.invalidateQueries({ queryKey: pickerRoot });
  });
  await waitFor(() => expect(select.validity.customError).toBe(true));
});
it("clears a prior selection when its scope key changes", async () => {
  const { updateScope } = mount();
  await screen.findByRole("option", { name: "Item 1" });
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "id-1" } });
  updateScope("two");
  expect(screen.getByRole("combobox")).toHaveValue("");
});
it("preserves an existing optional ID outside the first page", async () => {
  mount({ required: false, defaultValue: "id-226" });
  await screen.findByRole("option", { name: "Item 1" });
  expect(screen.getByRole("combobox")).toHaveValue("id-226");
});
