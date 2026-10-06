import { render, screen } from "@testing-library/react";
import { Lock } from "lucide-react";
import { InfoCallout } from "@/components/ui/info-callout";

describe("InfoCallout", () => {
  it("renders an info note with its message", () => {
    render(<InfoCallout>Releases are cluster-wide.</InfoCallout>);
    const note = screen.getByRole("note");
    expect(note).toHaveTextContent("Releases are cluster-wide.");
    expect(note.className).toContain("bg-status-info/10");
  });

  it("uses an alert role and warning styling for the warning tone", () => {
    render(<InfoCallout tone="warning">Careful</InfoCallout>);
    const alert = screen.getByRole("alert");
    expect(alert.className).toContain("bg-status-warning/10");
  });

  it("renders a custom icon (decorative) and an action", () => {
    const { container } = render(
      <InfoCallout icon={Lock} action={<button>Learn more</button>}>
        Locked
      </InfoCallout>,
    );
    expect(container.querySelector("svg")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
    expect(
      screen.getByRole("button", { name: "Learn more" }),
    ).toBeInTheDocument();
  });
});
