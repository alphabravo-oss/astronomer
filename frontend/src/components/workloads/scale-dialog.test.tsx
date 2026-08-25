import { fireEvent, render, screen } from "@testing-library/react";

import { ScaleDialog } from "./scale-dialog";

describe("ScaleDialog", () => {
  it("exposes keyboard-addressable replica controls and submits the value", () => {
    const onScale = vi.fn();
    render(
      <ScaleDialog
        open
        onClose={vi.fn()}
        onScale={onScale}
        workloadName="payments"
        currentReplicas={2}
      />,
    );

    const input = screen.getByRole("spinbutton", { name: "Desired replicas" });
    expect(input).toHaveValue(2);

    fireEvent.click(
      screen.getByRole("button", { name: "Increase desired replicas" }),
    );
    expect(input).toHaveValue(3);

    fireEvent.click(screen.getByRole("button", { name: "Scale" }));
    expect(onScale).toHaveBeenCalledWith(3);
  });

  it("clamps typed replica counts to the supported range", () => {
    render(
      <ScaleDialog
        open
        onClose={vi.fn()}
        onScale={vi.fn()}
        workloadName="payments"
        currentReplicas={2}
      />,
    );

    const input = screen.getByRole("spinbutton", { name: "Desired replicas" });
    fireEvent.change(input, { target: { value: "101" } });
    expect(input).toHaveValue(100);
    fireEvent.change(input, { target: { value: "-1" } });
    expect(input).toHaveValue(0);
  });
});
