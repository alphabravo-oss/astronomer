import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { SIEMForwarder } from "@/types";
import { SIEM_AUTH_SENTINEL } from "@/lib/api/siem-forwarders";
import { SIEMForwarderModal } from "./-forwarder-modal";
const { save } = vi.hoisted(() => ({ save: vi.fn() }));
vi.mock("./-hooks", () => ({
  useCreateSIEMForwarder: () => ({ mutateAsync: save, isPending: false }),
  useUpdateSIEMForwarder: () => ({ mutateAsync: save, isPending: false }),
}));
beforeEach(() => save.mockReset());
it("submits a semantic form once and keeps a failed save visible during editing", async () => {
  save.mockRejectedValueOnce(new Error("Endpoint refused connection"));
  const close = vi.fn();
  render(<SIEMForwarderModal forwarder={null} onClose={close} />);
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "corp" },
  });
  fireEvent.change(screen.getByLabelText("Endpoint"), {
    target: { value: "siem.example:6514" },
  });
  const form = screen.getByLabelText("Name").closest("form");
  expect(form).not.toBeNull();
  fireEvent.submit(form!);
  await waitFor(() =>
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Endpoint refused connection",
    ),
  );
  expect(save).toHaveBeenCalledTimes(1);
  expect(close).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "corrected" },
  });
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Endpoint refused connection",
  );
  save.mockResolvedValueOnce({});
  fireEvent.submit(form!);
  await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

it("retains stored auth on a blank edit and only replaces it after operator input", async () => {
  save.mockResolvedValue({});
  render(
    <SIEMForwarderModal
      forwarder={
        {
          id: "forwarder-1",
          name: "corp",
          endpoint: "siem.example:6514",
          authConfigured: true,
        } as SIEMForwarder
      }
      onClose={vi.fn()}
    />,
  );
  fireEvent.submit(screen.getByLabelText("Name").closest("form")!);
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  expect(save.mock.calls[0][0]).toMatchObject({
    id: "forwarder-1",
    body: { auth: SIEM_AUTH_SENTINEL },
  });
  fireEvent.change(screen.getByLabelText(/Auth/), {
    target: { value: "replacement-fixture" },
  });
  fireEvent.submit(screen.getByLabelText("Name").closest("form")!);
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2));
  expect(save.mock.calls[1][0]).toMatchObject({
    body: { auth: "replacement-fixture" },
  });
});
it("invalid submission focuses the summary and exposes links to required fields", async () => {
  render(<SIEMForwarderModal forwarder={null} onClose={vi.fn()} />);
  fireEvent.submit(screen.getByLabelText("Name").closest("form")!);
  await waitFor(() => expect(screen.getByRole("alert")).toHaveFocus());
  screen.getByLabelText("Name").scrollIntoView = vi.fn();
  fireEvent.click(screen.getByRole("button", { name: "Name is required" }));
  expect(screen.getByLabelText("Name")).toHaveFocus();
  expect(save).not.toHaveBeenCalled();
});
