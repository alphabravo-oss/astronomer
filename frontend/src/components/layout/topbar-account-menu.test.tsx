import { fireEvent, render, screen } from "@testing-library/react";
import { TopbarAccountMenu } from "./topbar-account-menu";

const navigate = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
}));
vi.mock("@/lib/store", () => ({
  useAuthStore: () => ({
    user: { displayName: "Ada", email: "ada@example.com" },
    logout: vi.fn(),
  }),
}));
vi.mock("@/lib/api/account-security", () => ({
  logoutCurrentSession: vi.fn(),
}));

function renderMenu() {
  return render(
    <header style={{ overflow: "hidden", height: 10 }}>
      <TopbarAccountMenu />
    </header>,
  );
}

describe("TopbarAccountMenu popover", () => {
  it("portals the panel out of an overflow-hidden header and tracks aria-expanded", async () => {
    renderMenu();
    const trigger = screen.getByRole("button", { name: "User menu" });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(trigger);
    const panel = await screen.findByRole("dialog");
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(panel.closest("header")).toBeNull();
    expect(panel).toHaveTextContent("ada@example.com");
  });

  it("closes on Escape and returns focus to the trigger", async () => {
    renderMenu();
    const trigger = screen.getByRole("button", { name: "User menu" });
    trigger.focus();
    fireEvent.click(trigger);
    const panel = await screen.findByRole("dialog");
    fireEvent.keyDown(panel, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await vi.waitFor(() => expect(trigger).toHaveFocus());
  });

  it("navigates and closes when an entry is chosen", async () => {
    renderMenu();
    fireEvent.click(screen.getByRole("button", { name: "User menu" }));
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    expect(navigate).toHaveBeenCalledWith({ to: "/dashboard/settings" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
