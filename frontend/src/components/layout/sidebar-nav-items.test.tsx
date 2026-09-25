import { fireEvent, render, screen } from "@testing-library/react";
import { Puzzle } from "lucide-react";
import { SidebarNavItems } from "./sidebar-nav-items";
import type { NavGroup } from "./sidebar-navigation";

vi.mock("@tanstack/react-router", async () => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    Link: RouterLinkStub,
    useLocation: ({
      select,
    }: {
      select: (value: { searchStr: string }) => unknown;
    }) => select({ searchStr: "" }),
  };
});

const group: NavGroup = {
  label: "More Resources",
  items: [],
  subgroups: [
    {
      label: "Cert Manager",
      items: [
        {
          label: "Certificate",
          href: "/certificates",
          icon: Puzzle,
          resourceType: "cert-manager.io/certificates",
          countKey: "crd:cert-manager.io/certificates",
        },
      ],
    },
  ],
};

it("renders nested navigation and a separate accessible star button", () => {
  const toggle = vi.fn();
  render(
    <SidebarNavItems
      group={group}
      pathname="/certificates"
      counts={{ "crd:cert-manager.io/certificates": 3 }}
      stars={{ types: [], disabled: false, toggle }}
    />,
  );
  expect(screen.getByText("Cert Manager")).toBeInTheDocument();
  const link = screen.getByRole("link", { name: /Certificate/ });
  expect(link).toHaveAttribute("aria-current", "page");
  const star = screen.getByRole("button", { name: "Star Certificate" });
  expect(star).toHaveAttribute("aria-pressed", "false");
  expect(link.contains(star)).toBe(false);
  fireEvent.click(star);
  expect(toggle).toHaveBeenCalledWith("cert-manager.io/certificates");
});

it("keeps unstar available at the limit while disabling additional stars", () => {
  const types = Array.from(
    { length: 20 },
    (_, index) => `test.io/type${index}`,
  );
  const { rerender } = render(
    <SidebarNavItems
      group={group}
      pathname="/"
      stars={{ types, disabled: false, toggle: vi.fn() }}
    />,
  );
  expect(
    screen.getByRole("button", { name: "Star Certificate" }),
  ).toBeDisabled();
  rerender(
    <SidebarNavItems
      group={group}
      pathname="/"
      stars={{
        types: ["cert-manager.io/certificates", ...types.slice(1)],
        disabled: false,
        toggle: vi.fn(),
      }}
    />,
  );
  expect(
    screen.getByRole("button", { name: "Unstar Certificate" }),
  ).toBeEnabled();
});
