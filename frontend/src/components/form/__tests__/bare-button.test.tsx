import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { BareButton } from "@/components/form/bare-button";
import { LoadingSkeleton } from "@/components/form/loading-skeleton";

describe("BareButton", () => {
  it("is a non-submitting button that keeps caller classes and handlers", () => {
    const onClick = vi.fn();
    render(
      <BareButton onClick={onClick} className="flex text-left">
        Open
      </BareButton>,
    );
    const button = screen.getByRole("button", { name: "Open" });
    expect(button).toHaveAttribute("type", "button");
    expect(button).toHaveClass("flex", "text-left");
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("derives an accessible name from the tooltip on icon-only controls", () => {
    render(<BareButton size="icon-xs" tooltip="Remove client" />);
    expect(
      screen.getByRole("button", { name: "Remove client" }),
    ).toBeInTheDocument();
  });
});

describe("LoadingSkeleton", () => {
  it("exposes one busy status region", () => {
    render(<LoadingSkeleton label="Loading widgets" lines={2} />);
    expect(
      screen.getByRole("status", { name: "Loading widgets" }),
    ).toHaveAttribute("aria-busy", "true");
  });
});
