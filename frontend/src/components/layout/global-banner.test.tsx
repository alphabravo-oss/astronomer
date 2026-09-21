import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const mockUseBanner = vi.hoisted(() => vi.fn());

vi.mock("@/lib/hooks/public-settings", () => ({
  useBanner: () => mockUseBanner(),
}));

import { GlobalBanner } from "./global-banner";

describe("GlobalBanner", () => {
  it("renders the banner text with the info tone by default", () => {
    mockUseBanner.mockReturnValue({
      data: {
        "banner.global_text": "Maintenance window 18:00 UTC tonight.",
        "banner.global_color": "info",
      },
    });
    render(<GlobalBanner />);
    const banner = screen.getByRole("status");
    expect(banner).toHaveTextContent("Maintenance window 18:00 UTC tonight.");
    expect(banner.className).toContain("bg-status-info/15");
    expect(banner.className).toContain("text-status-info");
  });

  it("renders the critical tone when configured", () => {
    mockUseBanner.mockReturnValue({
      data: {
        "banner.global_text": "Classified — handle per policy.",
        "banner.global_color": "critical",
      },
    });
    render(<GlobalBanner />);
    expect(screen.getByRole("status").className).toContain(
      "bg-status-error/15",
    );
  });

  it("renders nothing when there is no banner text", () => {
    mockUseBanner.mockReturnValue({
      data: { "banner.global_text": "", "banner.global_color": "info" },
    });
    const { container } = render(<GlobalBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the query has no data (e.g. it rejected)", () => {
    mockUseBanner.mockReturnValue({ data: undefined });
    const { container } = render(<GlobalBanner />);
    expect(container).toBeEmptyDOMElement();
  });
});
