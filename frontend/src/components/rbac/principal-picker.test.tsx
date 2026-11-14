import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PrincipalPicker } from "./principal-picker";

vi.mock("@/lib/hooks/rbac", () => ({
  usePrincipalSearch: vi.fn(() => ({
    data: {
      principals: [
        {
          kind: "local",
          user_id: "user-local",
          email: "local@example.com",
          username: "local",
          display_name: "Local Operator",
        },
      ],
      connectors: [],
    },
    isFetching: false,
    isError: false,
  })),
}));
describe("PrincipalPicker", () => {
  it("selects an existing local account without creating an external principal", () => {
    const onChange = vi.fn();
    render(<PrincipalPicker value="" onChange={onChange} />);
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Search local accounts" }),
      { target: { value: "local" } },
    );
    fireEvent.click(screen.getByText("Local Operator"));
    expect(onChange).toHaveBeenCalledWith("user-local");
  });
});
