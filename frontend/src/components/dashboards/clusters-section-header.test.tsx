import { render, screen } from "@testing-library/react";

vi.mock("@tanstack/react-router", async (importOriginal) => {
  const { RouterLinkStub } = await import("@/test/router-link");
  return {
    ...(await importOriginal<typeof import("@tanstack/react-router")>()),
    Link: RouterLinkStub,
  };
});

import { ClustersSectionHeader } from "@/components/dashboards/clusters-section-header";

describe("ClustersSectionHeader", () => {
  it("shows just View all when the page holds everything", () => {
    render(<ClustersSectionHeader shown={5} total={5} />);
    expect(screen.getByText("View all")).toBeInTheDocument();
    expect(screen.queryByText(/showing/i)).not.toBeInTheDocument();
  });

  it("shows the truncation count when the estate is bigger than one page", () => {
    render(<ClustersSectionHeader shown={10} total={25} />);
    expect(screen.getByText(/Showing 10 of 25 clusters/)).toBeInTheDocument();
    expect(screen.getByText("View all")).toBeInTheDocument();
  });
});
