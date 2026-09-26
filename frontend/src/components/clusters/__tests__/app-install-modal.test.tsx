import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AppInstallModal } from "@/components/clusters/app-install-modal";
import {
  getChartDefaultValues,
  previewCatalogApplication,
} from "@/lib/api/cluster-apps";

const catalogVersions = vi.hoisted(() => ({
  data: {
    data: [
      {
        id: "version-1",
        chartId: "chart-1",
        version: "91.5.2",
        appVersion: "0.86.0",
        createdAt: "2026-09-26T00:00:00Z",
      },
    ],
    pagination: {
      limit: 25,
      offset: 0,
      has_more: false,
      next_offset: null,
    },
  },
  isError: false,
  isLoading: false,
  isFetching: false,
  refetch: vi.fn(),
}));

vi.mock("@/lib/hooks/catalog", () => ({
  useHelmChartVersions: () => catalogVersions,
}));

vi.mock("@/lib/api/cluster-apps", () => ({
  getChartDefaultValues: vi.fn(),
  installChartOnCluster: vi.fn(),
  previewCatalogApplication: vi.fn(),
  upgradeClusterApp: vi.fn(),
  getInstalledAppValues: vi.fn(),
}));

function renderModal() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AppInstallModal
        projectId="project-1"
        clusterId="cluster-1"
        mode={{
          kind: "install",
          chartId: "chart-1",
          chartName: "kube-prometheus-stack",
        }}
        onClose={vi.fn()}
      />
    </QueryClientProvider>,
  );
}

describe("AppInstallModal", () => {
  beforeEach(() => {
    vi.mocked(getChartDefaultValues).mockResolvedValue({
      chart: "kube-prometheus-stack",
      version: "91.5.2",
      defaultValues:
        "prometheus:\n  prometheusSpec:\n    scrapeInterval: 30s\nprometheusOperator:\n  enabled: true\n",
      valuesSchema: {
        type: "object",
        properties: {
          prometheus: {
            type: "object",
            properties: {
              prometheusSpec: {
                type: "object",
                properties: {
                  scrapeInterval: { type: "string" },
                },
              },
            },
          },
        },
      },
    });
    vi.mocked(previewCatalogApplication).mockResolvedValue({
      allowed: true,
      checks: [],
      application: "kube-prometheus-stack",
      artifact_digest: `sha256:${"a".repeat(64)}`,
      values_digest: `sha256:${"b".repeat(64)}`,
      catalog_digest: `sha256:${"c".repeat(64)}`,
    });
  });

  it("hydrates a fresh install into the curated form and YAML editor", async () => {
    renderModal();

    expect(await screen.findByRole("button", { name: "Form" })).toBeVisible();
    expect(
      screen.getByRole("textbox", { name: "Scrape interval" }),
    ).toHaveValue("30s");
    expect(
      screen.getByRole("checkbox", { name: "Deploy Prometheus Operator" }),
    ).toBeChecked();

    fireEvent.click(screen.getByRole("button", { name: "YAML" }));
    await waitFor(() =>
      expect(
        (
          screen.getByRole("textbox", {
            name: "Values (YAML)",
          }) as HTMLTextAreaElement
        ).value,
      ).toContain("scrapeInterval: 30s"),
    );
  });
});
