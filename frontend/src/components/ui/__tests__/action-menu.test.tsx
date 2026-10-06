import { fireEvent, render, screen } from "@testing-library/react";
import { ActionMenu } from "@/components/ui/action-menu";

function setup() {
  const onEdit = vi.fn();
  const onDelete = vi.fn();
  const onRowClick = vi.fn();
  render(
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events, jsx-a11y/no-static-element-interactions
    <div style={{ overflow: "hidden", height: 10 }} onClick={onRowClick}>
      <ActionMenu
        ariaLabel="Row actions"
        items={[
          { label: "Edit", onClick: onEdit },
          { label: "Clone", onClick: vi.fn() },
          {
            label: "Scale",
            onClick: vi.fn(),
            disabled: true,
            disabledReason: "Read-only role",
          },
          {
            label: "Delete",
            onClick: onDelete,
            variant: "destructive",
            separator: true,
          },
        ]}
      />
    </div>,
  );
  return { onEdit, onDelete, onRowClick };
}

describe("ActionMenu", () => {
  it("renders in a portal outside an overflow-hidden parent", () => {
    const { onRowClick } = setup();
    const trigger = screen.getByRole("button", { name: "Row actions" });
    expect(trigger).toHaveAttribute("aria-haspopup", "menu");
    fireEvent.click(trigger);
    const menu = screen.getByRole("menu");
    expect(menu.closest("[style*='overflow']")).toBeNull();
    expect(menu.parentElement?.parentElement).toBe(document.body);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(onRowClick).not.toHaveBeenCalled();
  });

  it("opens from the keyboard, runs an item and returns focus to the trigger", async () => {
    const { onEdit } = setup();
    const trigger = screen.getByRole("button", { name: "Row actions" });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "Enter" });
    const edit = await screen.findByRole("menuitem", { name: "Edit" });
    expect(edit).toHaveFocus();
    fireEvent.click(edit);
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("closes on Escape and restores trigger focus", async () => {
    setup();
    const trigger = screen.getByRole("button", { name: "Row actions" });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "Enter" });
    const edit = await screen.findByRole("menuitem", { name: "Edit" });
    fireEvent.keyDown(edit, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
    await vi.waitFor(() => expect(trigger).toHaveFocus());
  });

  it("moves with arrow keys and type-ahead, skipping disabled items", async () => {
    setup();
    const trigger = screen.getByRole("button", { name: "Row actions" });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "Enter" });
    const item = (name: string) => screen.getByRole("menuitem", { name });
    fireEvent.keyDown(await screen.findByRole("menuitem", { name: "Edit" }), {
      key: "ArrowDown",
    });
    await vi.waitFor(() => expect(item("Clone")).toHaveFocus());
    fireEvent.keyDown(item("Clone"), { key: "ArrowDown" });
    // Scale is disabled, so focus skips to Delete.
    await vi.waitFor(() => expect(item("Delete")).toHaveFocus());
    fireEvent.keyDown(item("Delete"), { key: "e" });
    await vi.waitFor(() => expect(item("Edit")).toHaveFocus());
  });

  it("keeps disabled items inert and explains why via the native title", () => {
    const { onEdit } = setup();
    fireEvent.click(screen.getByRole("button", { name: "Row actions" }));
    const scale = screen.getByRole("menuitem", { name: "Scale" });
    expect(scale).toBeDisabled();
    expect(scale).toHaveAttribute("title", "Read-only role");
    fireEvent.click(scale);
    expect(onEdit).not.toHaveBeenCalled();
    expect(screen.getByRole("menu")).toBeInTheDocument();
  });

  it("renders a separator only before flagged items", () => {
    setup();
    fireEvent.click(screen.getByRole("button", { name: "Row actions" }));
    expect(screen.getAllByRole("separator")).toHaveLength(1);
  });
});
