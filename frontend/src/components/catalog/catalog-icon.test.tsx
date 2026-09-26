import { render, screen } from "@testing-library/react";
import {
  CatalogIcon,
  isCatalogIconSourceAllowed,
} from "@/components/catalog/catalog-icon";

describe("CatalogIcon", () => {
  it("does not request remote icons blocked by the application CSP", () => {
    render(
      <CatalogIcon
        src="https://charts.example.test/prometheus.png"
        label="Prometheus"
      />,
    );

    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(screen.getByText("P")).toBeVisible();
  });

  it("allows same-origin and image data sources", () => {
    expect(isCatalogIconSourceAllowed("/catalog-icons/prometheus.svg")).toBe(
      true,
    );
    expect(
      isCatalogIconSourceAllowed(
        `${window.location.origin}/catalog-icons/prometheus.svg`,
      ),
    ).toBe(true);
    expect(
      isCatalogIconSourceAllowed("data:image/svg+xml;base64,PHN2Zy8+"),
    ).toBe(true);
  });
});
