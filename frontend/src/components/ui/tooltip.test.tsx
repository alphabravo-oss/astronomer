import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Tooltip } from "@/components/ui/tooltip";

describe("Tooltip", () => {
  it("shows content on keyboard focus", async () => {
    render(
      <Tooltip content="Explains the control">
        <button type="button">Trigger</button>
      </Tooltip>,
    );
    fireEvent.focus(screen.getByRole("button", { name: "Trigger" }));
    expect(await screen.findAllByText("Explains the control")).not.toHaveLength(
      0,
    );
  });

  it("renders the trigger untouched when content is empty", () => {
    render(
      <Tooltip content="">
        <button type="button">Plain</button>
      </Tooltip>,
    );
    expect(screen.getByRole("button", { name: "Plain" })).toBeInTheDocument();
  });

  it("wraps disabled triggers so the reason stays reachable", async () => {
    render(
      <Tooltip content="Needs admin" wrap>
        <button type="button" disabled>
          Delete
        </button>
      </Tooltip>,
    );
    fireEvent.focus(
      screen.getByRole("button", { name: "Delete" }).parentElement!,
    );
    expect(await screen.findAllByText("Needs admin")).not.toHaveLength(0);
  });
});
