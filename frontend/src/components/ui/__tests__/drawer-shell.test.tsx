import { fireEvent, render, screen } from "@testing-library/react";
import { flushSync } from "react-dom";
import { DrawerShell } from "@/components/ui/drawer-shell";

describe("DrawerShell", () => {
  it("keeps Escape handling registered when an earlier global listener rerenders its parent", () => {
    const firstClose = vi.fn();
    const nextClose = vi.fn();
    const globalKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") flushSync(() => changeParent());
    };
    document.addEventListener("keydown", globalKey);
    const view = (onClose: () => void) => (
      <DrawerShell title="Report details" onClose={onClose}>
        Report
      </DrawerShell>
    );
    const { rerender, unmount } = render(view(firstClose));
    const changeParent = () => rerender(view(nextClose));
    try {
      fireEvent.keyDown(document, { key: "Escape" });
      expect(nextClose).toHaveBeenCalledTimes(1);
      expect(firstClose).not.toHaveBeenCalled();
    } finally {
      document.removeEventListener("keydown", globalKey);
      unmount();
    }
  });

  it("escapes page stacking contexts and restores focus when unmounted", () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();
    const onClose = vi.fn();
    const { container, unmount } = render(
      <div style={{ transform: "translateX(0)", opacity: 0.9 }}>
        <DrawerShell title="Report details" onClose={onClose}>
          <button type="button">Next page</button>
        </DrawerShell>
      </div>,
    );
    const dialog = screen.getByRole("dialog", { name: "Report details" });
    expect(container).not.toContainElement(dialog);
    expect(dialog.closest("[data-overlay-root]")?.parentElement).toBe(
      document.body,
    );
    expect(screen.getByRole("button", { name: "Close" })).toHaveFocus();
    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(screen.getByRole("button", { name: "Next page" })).toHaveFocus();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
    unmount();
    expect(trigger).toHaveFocus();
    trigger.remove();
  });
});
