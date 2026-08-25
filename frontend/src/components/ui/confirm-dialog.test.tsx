import { fireEvent, render, screen } from "@testing-library/react";
import { ConfirmDialog } from "./confirm-dialog";

describe("ConfirmDialog", () => {
  it("shows impact before the confirmation input and requires an exact typed value", () => {
    const onConfirm = vi.fn();

    render(
      <ConfirmDialog
        open
        onClose={vi.fn()}
        onConfirm={onConfirm}
        title="Delete Namespace"
        description="This action cannot be undone."
        variant="destructive"
        confirmValue="production"
        impact={{
          scope: "Namespace production",
          consequences: [
            "All namespaced resources will be deleted.",
            "Storage retention depends on reclaim policy.",
          ],
          recovery: "Restore from Git and backup.",
        }}
      />,
    );

    const impact = screen.getByRole("region", { name: "Impact preview" });
    const input = screen.getByPlaceholderText("production");
    const confirm = screen.getByRole("button", { name: "Delete" });

    expect(impact).toHaveTextContent("Namespace production");
    expect(impact).toHaveTextContent("All namespaced resources will be deleted.");
    expect(impact).toHaveTextContent("Restore from Git and backup.");
    expect(impact.compareDocumentPosition(input)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(confirm).toBeDisabled();

    fireEvent.change(input, { target: { value: "Production" } });
    expect(confirm).toBeDisabled();

    fireEvent.change(input, { target: { value: "production" } });
    expect(confirm).toBeEnabled();
    fireEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("resets typed confirmation when the dialog is closed", () => {
    const props = {
      onClose: vi.fn(),
      onConfirm: vi.fn(),
      title: "Delete Pod",
      description: "Delete the pod.",
      variant: "destructive" as const,
      confirmValue: "api-0",
    };
    const { rerender } = render(<ConfirmDialog {...props} open />);

    fireEvent.change(screen.getByPlaceholderText("api-0"), {
      target: { value: "api-0" },
    });
    expect(screen.getByRole("button", { name: "Delete" })).toBeEnabled();

    rerender(<ConfirmDialog {...props} open={false} />);
    rerender(<ConfirmDialog {...props} open />);

    expect(screen.getByPlaceholderText("api-0")).toHaveValue("");
    expect(screen.getByRole("button", { name: "Delete" })).toBeDisabled();
  });
});
