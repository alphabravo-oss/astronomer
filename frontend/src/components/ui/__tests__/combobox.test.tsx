import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import {
  COMBOBOX_VIRTUALIZE_AFTER,
  Combobox,
  type ComboboxOption,
} from "@/components/ui/combobox";

const small: ComboboxOption[] = [
  { value: "default", label: "default", description: "Default namespace" },
  { value: "kube-system", label: "kube-system" },
  { value: "monitoring", label: "monitoring", description: "Prometheus" },
];

function Harness({
  options = small,
  initial = null,
  ...rest
}: Partial<React.ComponentProps<typeof Combobox>> & {
  initial?: string | null;
}) {
  const [value, setValue] = useState<string | null>(initial);
  return (
    <div style={{ overflow: "hidden", height: 20 }}>
      <Combobox
        aria-label="Namespace"
        options={options}
        value={value}
        onValueChange={setValue}
        placeholder="Pick namespace"
        {...rest}
      />
    </div>
  );
}

describe("Combobox", () => {
  it("shows the selected label or the placeholder", () => {
    const { unmount } = render(<Harness />);
    expect(screen.getByRole("button", { name: "Namespace" })).toHaveTextContent(
      "Pick namespace",
    );
    unmount();
    render(<Harness initial="monitoring" />);
    expect(screen.getByRole("button", { name: "Namespace" })).toHaveTextContent(
      "monitoring",
    );
  });

  it("opens in a body portal, filters locally and selects with the keyboard", async () => {
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Namespace" });
    fireEvent.click(trigger);
    const search = await screen.findByRole("combobox", {
      name: "Search Namespace",
    });
    const list = screen.getByRole("listbox", { name: "Namespace" });
    expect(list.closest("[style*='overflow: hidden']")).toBeNull();
    expect(search).toHaveFocus();
    expect(within(list).getAllByRole("option")).toHaveLength(3);

    fireEvent.change(search, { target: { value: "prom" } });
    const options = within(list).getAllByRole("option");
    expect(options).toHaveLength(1);
    expect(search).toHaveAttribute("aria-activedescendant", options[0].id);

    fireEvent.keyDown(search, { key: "Enter" });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(trigger).toHaveTextContent("monitoring");
  });

  it("navigates with arrows and returns focus to the trigger on Escape", async () => {
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Namespace" });
    trigger.focus();
    fireEvent.click(trigger);
    const search = await screen.findByRole("combobox", {
      name: "Search Namespace",
    });
    fireEvent.keyDown(search, { key: "ArrowDown" });
    expect(screen.getAllByRole("option")[1]).toHaveAttribute(
      "data-active",
      "true",
    );
    fireEvent.keyDown(search, { key: "End" });
    expect(screen.getAllByRole("option")[2]).toHaveAttribute(
      "data-active",
      "true",
    );
    fireEvent.keyDown(search, { key: "Escape" });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    await vi.waitFor(() => expect(trigger).toHaveFocus());
  });

  it("delegates filtering to onSearchChange and shows loading and empty states", async () => {
    const onSearchChange = vi.fn();
    const { rerender } = render(
      <Harness onSearchChange={onSearchChange} loading emptyText="Nothing" />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Namespace" }));
    const search = await screen.findByRole("combobox", {
      name: "Search Namespace",
    });
    fireEvent.change(search, { target: { value: "zzz" } });
    expect(onSearchChange).toHaveBeenLastCalledWith("zzz");
    // Server-side mode keeps the options the caller supplies.
    expect(screen.getAllByRole("option")).toHaveLength(3);
    expect(screen.getByRole("status")).toHaveTextContent("Loading");
    expect(screen.getByRole("listbox")).toHaveAttribute("aria-busy", "true");
    rerender(
      <Harness
        onSearchChange={onSearchChange}
        options={[]}
        emptyText="Nothing"
      />,
    );
    expect(screen.getByRole("status")).toHaveTextContent("Nothing");
  });

  it("shows the empty text when local filtering matches nothing", async () => {
    render(<Harness emptyText="No namespaces" />);
    fireEvent.click(screen.getByRole("button", { name: "Namespace" }));
    fireEvent.change(await screen.findByRole("combobox"), {
      target: { value: "nope" },
    });
    expect(screen.getByText("No namespaces")).toBeInTheDocument();
  });

  it("does not open when disabled", () => {
    render(<Harness disabled />);
    const trigger = screen.getByRole("button", { name: "Namespace" });
    expect(trigger).toBeDisabled();
    fireEvent.click(trigger);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  describe("virtualization", () => {
    const original = Object.getOwnPropertyDescriptor(
      HTMLElement.prototype,
      "offsetHeight",
    );
    const originalWidth = Object.getOwnPropertyDescriptor(
      HTMLElement.prototype,
      "offsetWidth",
    );
    const originalRO = globalThis.ResizeObserver;
    beforeEach(() => {
      Object.defineProperty(HTMLElement.prototype, "offsetHeight", {
        configurable: true,
        value: 280,
      });
      Object.defineProperty(HTMLElement.prototype, "offsetWidth", {
        configurable: true,
        value: 300,
      });
      globalThis.ResizeObserver = class {
        observe() {}
        unobserve() {}
        disconnect() {}
      } as unknown as typeof ResizeObserver;
    });
    afterEach(() => {
      if (original)
        Object.defineProperty(HTMLElement.prototype, "offsetHeight", original);
      if (originalWidth)
        Object.defineProperty(
          HTMLElement.prototype,
          "offsetWidth",
          originalWidth,
        );
      globalThis.ResizeObserver = originalRO;
    });

    it("windows rows once past the threshold", async () => {
      const many = Array.from(
        { length: COMBOBOX_VIRTUALIZE_AFTER + 400 },
        (_, i) => ({ value: `ns-${i}`, label: `ns-${i}` }),
      );
      render(<Harness options={many} />);
      fireEvent.click(screen.getByRole("button", { name: "Namespace" }));
      await screen.findByRole("listbox");
      await act(async () => {});
      const rendered = screen.getAllByRole("option");
      expect(rendered.length).toBeGreaterThan(0);
      expect(rendered.length).toBeLessThan(60);
    });

    it("renders every row at or below the threshold", async () => {
      const exact = Array.from(
        { length: COMBOBOX_VIRTUALIZE_AFTER },
        (_, i) => ({ value: `ns-${i}`, label: `ns-${i}` }),
      );
      render(<Harness options={exact} />);
      fireEvent.click(screen.getByRole("button", { name: "Namespace" }));
      await screen.findByRole("listbox");
      expect(screen.getAllByRole("option")).toHaveLength(
        COMBOBOX_VIRTUALIZE_AFTER,
      );
    });
  });
});
