import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { useAppForm } from "@/lib/form";

function Harness({ serverError }: { serverError?: string }) {
  const form = useAppForm({ defaultValues: { email: "" }, onSubmit: vi.fn() });
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void form.handleSubmit();
      }}
    >
      <form.AppForm>
        <form.FormErrorSummary serverError={serverError} />
      </form.AppForm>
      <form.AppField
        name="email"
        validators={{
          onSubmit: ({ value }) => (value ? undefined : "Email is required"),
        }}
      >
        {(field) => <field.TextField label="Email" />}
      </form.AppField>
      <button type="submit">Save</button>
    </form>
  );
}

describe("FormErrorSummary", () => {
  it("announces failed validation and lets the operator focus the invalid control", async () => {
    render(<Harness />);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "Save" })),
    );
    const summary = screen.getByRole("alert");
    expect(summary).toHaveFocus();
    const control = screen.getByLabelText("Email");
    control.scrollIntoView = vi.fn();
    fireEvent.click(
      within(summary).getByRole("button", { name: "Email is required" }),
    );
    expect(control).toHaveFocus();
    expect(control).toHaveAttribute("aria-invalid", "true");
  });

  it("keeps a server failure visible even when local fields are valid", () => {
    render(<Harness serverError="The current password is incorrect." />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "The current password is incorrect.",
    );
    expect(screen.getByRole("alert")).toHaveFocus();
  });
});
