import { fireEvent, render, screen } from "@testing-library/react";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

vi.mock("@/lib/hooks/public-settings", () => ({
  useProductName: () => "Astronomer",
}));

import { WelcomeBanner } from "@/components/dashboards/welcome-banner";

const DISMISS_KEY = "astronomer.home.welcomeDismissed";

describe("WelcomeBanner", () => {
  beforeEach(() => {
    localStorage.removeItem(DISMISS_KEY);
  });

  it("renders the product name and docs link", () => {
    render(<WelcomeBanner estateEmpty={false} />);
    expect(screen.getByText("Welcome to Astronomer")).toBeInTheDocument();
    expect(screen.getByText("Read the docs")).toBeInTheDocument();
  });

  it("offers to register the first cluster only when the estate is empty", () => {
    const { rerender } = render(<WelcomeBanner estateEmpty={true} />);
    expect(
      screen.getByText("Register your first cluster"),
    ).toBeInTheDocument();

    rerender(<WelcomeBanner estateEmpty={false} />);
    expect(
      screen.queryByText("Register your first cluster"),
    ).not.toBeInTheDocument();
  });

  it("dismisses and persists across a remount", () => {
    const { unmount } = render(<WelcomeBanner estateEmpty={false} />);
    fireEvent.click(screen.getByLabelText("Dismiss welcome message"));
    expect(screen.queryByText(/Welcome to/)).not.toBeInTheDocument();
    expect(localStorage.getItem(DISMISS_KEY)).toBe("true");

    unmount();
    render(<WelcomeBanner estateEmpty={false} />);
    expect(screen.queryByText(/Welcome to/)).not.toBeInTheDocument();
  });
});
