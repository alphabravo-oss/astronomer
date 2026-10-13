import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { getGenericResources } from "@/lib/api/resource-search";
import {
  CatalogResourceContext,
  CatalogResourceField,
} from "./catalog-resource-field";

vi.mock("@/lib/api/resource-search", () => ({ getGenericResources: vi.fn() }));
function renderSecret(namespace = "logs") {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <CatalogResourceContext.Provider
        value={{ clusterId: "cluster-a", namespace }}
      >
        <CatalogResourceField
          id="secret-reference"
          kind="secrets"
          value="saved-secret"
          required={false}
          onChange={vi.fn()}
        />
      </CatalogResourceContext.Provider>
    </QueryClientProvider>,
  );
}
beforeEach(() => vi.resetAllMocks());
it("queries only the selected cluster/namespace metadata and preserves an off-page reference", async () => {
  vi.mocked(getGenericResources).mockResolvedValue({
    data: [
      {
        name: "visible-secret",
        namespace: "logs",
        clusterId: "cluster-a",
        labels: {},
        annotations: {},
        createdAt: "2026-10-12T00:00:00Z",
      },
    ],
    pagination: { limit: 25, offset: 0, has_more: true, next_offset: 25 },
  });
  renderSecret();
  expect(
    await screen.findByRole("option", { name: "visible-secret" }),
  ).toBeVisible();
  expect(
    screen.getByRole("option", { name: "saved-secret (current value)" }),
  ).toBeVisible();
  expect(getGenericResources).toHaveBeenCalledWith(
    "cluster-a",
    "secrets",
    expect.objectContaining({
      namespace: "logs",
      limit: 25,
      signal: expect.any(AbortSignal),
    }),
  );
  expect(screen.getByRole("button", { name: "More resources" })).toBeVisible();
});
it("keeps the current reference when access is denied", async () => {
  vi.mocked(getGenericResources).mockRejectedValue(new Error("forbidden"));
  renderSecret();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Check your access",
  );
  expect(screen.getByRole("combobox")).toBeDisabled();
  expect(screen.getByRole("combobox")).toHaveValue("saved-secret");
});
it("does not issue a cluster-wide Secret query without a namespace", async () => {
  renderSecret("");
  await waitFor(() => expect(getGenericResources).not.toHaveBeenCalled());
});
