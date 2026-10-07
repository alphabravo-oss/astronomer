import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";

describe("Dialog", () => {
  it("opens from its trigger in a body portal, closes on Escape and returns focus", async () => {
    render(
      <div style={{ overflow: "hidden", height: 10 }}>
        <Dialog>
          <DialogTrigger>Open dialog</DialogTrigger>
          <DialogContent>
            <DialogTitle>Rename</DialogTitle>
            <DialogDescription>Pick a name.</DialogDescription>
            <button type="button">Save</button>
          </DialogContent>
        </Dialog>
      </div>,
    );
    const trigger = screen.getByRole("button", { name: "Open dialog" });
    trigger.focus();
    fireEvent.click(trigger);
    const dialog = await screen.findByRole("dialog", { name: "Rename" });
    expect(dialog.parentElement).toBe(document.body);
    expect(dialog).toContainElement(
      screen.getByRole("button", { name: "Save" }),
    );
    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await vi.waitFor(() => expect(trigger).toHaveFocus());
  });
});

describe("Sheet", () => {
  it("anchors to the right edge and is labelled", async () => {
    render(
      <Sheet>
        <SheetTrigger>Open sheet</SheetTrigger>
        <SheetContent>
          <SheetTitle>Details</SheetTitle>
          <SheetDescription>Resource details.</SheetDescription>
        </SheetContent>
      </Sheet>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Open sheet" }));
    const sheet = await screen.findByRole("dialog", { name: "Details" });
    expect(sheet).toHaveClass("right-0");
  });
});

describe("Radix-backed shells", () => {
  it("locks body scroll and hides the page behind a ConfirmDialog", () => {
    function Page() {
      const [open, setOpen] = useState(true);
      return (
        <>
          <main>Page content</main>
          <ConfirmDialog
            open={open}
            onClose={() => setOpen(false)}
            onConfirm={vi.fn()}
            title="Delete"
            description="Really?"
          />
        </>
      );
    }
    render(<Page />);
    expect(screen.getByRole("dialog", { name: "Delete" })).toBeInTheDocument();
    // Radix marks everything outside the layer aria-hidden (modal semantics).
    expect(screen.queryByRole("main")).not.toBeInTheDocument();
    expect(document.body.style.pointerEvents).toBe("none");
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.getByRole("main")).toBeInTheDocument();
  });
});
