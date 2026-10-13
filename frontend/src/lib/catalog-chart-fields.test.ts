import { recommendedCatalogNamespace } from "@/lib/catalog-chart-fields";

it("uses dedicated namespaces for supported operator charts", () => {
  expect(recommendedCatalogNamespace("constellation")).toBe(
    "astronomer-constellation",
  );
  expect(recommendedCatalogNamespace("cert-manager")).toBe(
    "astronomer-cert-manager",
  );
  expect(recommendedCatalogNamespace("cloudnative-pg")).toBe("cnpg-system");
  expect(recommendedCatalogNamespace("unknown-chart")).toBe("default");
});
