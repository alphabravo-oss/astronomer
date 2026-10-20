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

function openMenu() {
  const trigger = screen.getByRole("button", { name: "User menu" });
  // Radix menus open from the keyboard (pointer activation needs real
  // pointer events, which jsdom does not synthesise).
  fireEvent.keyDown(trigger, { key: "Enter" });
  return trigger;
}

describe("TopbarAccountMenu menu", () => {
  it("portals the menu out of an overflow-hidden header and tracks aria-expanded", async () => {
    renderMenu();
    const trigger = screen.getByRole("button", { name: "User menu" });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    openMenu();
    const menu = await screen.findByRole("menu");
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(menu.closest("header")).toBeNull();
    expect(menu).toHaveTextContent("ada@example.com");
  });

  it("closes on Escape and returns focus to the trigger", async () => {
    renderMenu();
    const trigger = openMenu();
    const menu = await screen.findByRole("menu");
    fireEvent.keyDown(menu, { key: "Escape" });
    await vi.waitFor(() =>
      expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
    );
    await vi.waitFor(() => expect(trigger).toHaveFocus());
  });

  it("navigates and closes when an entry is chosen", async () => {
    renderMenu();
    openMenu();
    await screen.findByRole("menu");
    const item = screen.getByRole("menuitem", { name: "Settings" });
    fireEvent.keyDown(item, { key: "Enter" });
    expect(navigate).toHaveBeenCalledWith({ to: "/dashboard/settings" });
    await vi.waitFor(() =>
      expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
    );
  });
});
