import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState, type FormEvent } from "react";
import { ModalShell } from "@/components/ui/modal-shell";

describe("ModalShell", () => {
  it("prevents a nested picker search from implicitly submitting its parent form", () => {
    render(
      <ModalShell title="Parent form" onClose={vi.fn()} onSubmit={vi.fn()}>
        <input aria-label="Parent field" />
        <ModalShell title="Nested search" onClose={vi.fn()}>
          <input aria-label="Search targets" />
        </ModalShell>
      </ModalShell>,
    );
    expect(
      fireEvent.keyDown(screen.getByLabelText("Search targets"), {
        key: "Enter",
      }),
    ).toBe(false);
    expect(
      fireEvent.keyDown(screen.getByLabelText("Parent field"), {
        key: "Enter",
      }),
    ).toBe(true);
  });
  it("keeps the parent form open and restores picker-trigger focus on nested Escape", () => {
    const parentClose = vi.fn();
    function Nested() {
      const [open, setOpen] = useState(false);
      return (
        <ModalShell title="Parent form" onClose={parentClose}>
          <input aria-label="Draft name" defaultValue="Keep my draft" />
          <button type="button" onClick={() => setOpen(true)}>
            Open picker
          </button>
          {open && (
            <ModalShell title="Child picker" onClose={() => setOpen(false)}>
              <button type="button">Select target</button>
            </ModalShell>
          )}
        </ModalShell>
      );
    }
    render(<Nested />);
    const trigger = screen.getByRole("button", { name: "Open picker" });
    trigger.focus();
    fireEvent.click(trigger);
    const child = screen.getByRole("dialog", { name: "Child picker" });
    within(child).getByLabelText("Close").focus();
    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(
      within(child).getByRole("button", { name: "Select target" }),
    ).toHaveFocus();
    fireEvent.keyDown(document, { key: "Tab" });
    expect(within(child).getByLabelText("Close")).toHaveFocus();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(parentClose).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("dialog", { name: "Child picker" }),
    ).not.toBeInTheDocument();
    expect(screen.getByLabelText("Draft name")).toHaveValue("Keep my draft");
    expect(trigger).toHaveFocus();
    fireEvent.keyDown(document, { key: "Escape" });
    expect(parentClose).toHaveBeenCalledTimes(1);
  });
  it("renders title and body content", () => {
    render(
      <ModalShell title="Security action" onClose={vi.fn()}>
        <p>Confirm the sensitive action.</p>
      </ModalShell>,
    );

    expect(
      screen.getByRole("dialog", { name: "Security action" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Security action" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Confirm the sensitive action."),
    ).toBeInTheDocument();
  });

  it("closes on Escape", () => {
    const onClose = vi.fn();
    render(
      <ModalShell title="Security action" onClose={onClose}>
        <p>Confirm the sensitive action.</p>
      </ModalShell>,
    );

    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("moves focus into the dialog and restores prior focus on unmount", () => {
    const opener = document.createElement("button");
    opener.textContent = "Open modal";
    document.body.appendChild(opener);
    opener.focus();

    const { unmount } = render(
      <ModalShell title="Security action" onClose={vi.fn()}>
        <button type="button">Confirm action</button>
      </ModalShell>,
    );

    expect(screen.getByLabelText("Close")).toHaveFocus();

    unmount();
    expect(opener).toHaveFocus();
    opener.remove();
  });

  it("traps Tab focus inside the dialog", () => {
    render(
      <ModalShell
        title="Security action"
        onClose={vi.fn()}
        footer={
          <>
            <button type="button">Cancel</button>
            <button type="button">Submit</button>
          </>
        }
      >
        <p>Confirm the sensitive action.</p>
      </ModalShell>,
    );

    const close = screen.getByLabelText("Close");
    const submit = screen.getByRole("button", { name: "Submit" });

    expect(close).toHaveFocus();

    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(submit).toHaveFocus();

    fireEvent.keyDown(document, { key: "Tab" });
    expect(close).toHaveFocus();
  });

  it("honors managed initial focus on a field inside the dialog", () => {
    render(
      <ModalShell title="Type to confirm" onClose={vi.fn()}>
        <input aria-label="Confirmation" data-initial-focus />
      </ModalShell>,
    );

    expect(screen.getByLabelText("Confirmation")).toHaveFocus();
  });

  it("hosts body and footer controls in one semantic form", () => {
    const onSubmit = vi.fn((event: FormEvent<HTMLFormElement>) =>
      event.preventDefault(),
    );
    render(
      <ModalShell
        title="Create binding"
        onClose={vi.fn()}
        onSubmit={onSubmit}
        footer={<button type="submit">Create</button>}
      >
        <input aria-label="Binding name" />
      </ModalShell>,
    );

    const form = screen.getByRole("button", { name: "Create" }).closest("form");
    expect(form).toContainElement(screen.getByLabelText("Binding name"));
    fireEvent.submit(form!);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });
});
