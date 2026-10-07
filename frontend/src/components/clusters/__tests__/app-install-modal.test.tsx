import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ComponentProps } from "react";
import { queryKeys } from "@/lib/query-keys";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { AppInstallModal } from "@/components/clusters/app-install-modal";
import {
  getChartDefaultValues,
  getClusterAppValues,
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
  getClusterAppValues: vi.fn(),
}));

type Mode = ComponentProps<typeof AppInstallModal>["mode"];
const installMode: Mode = {
  kind: "install",
  chartId: "chart-1",
  chartName: "kube-prometheus-stack",
};
function renderModal(mode: Mode = installMode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const tree = (next: Mode) => (
    <QueryClientProvider client={client}>
      <AppInstallModal
        projectId="project-1"
        clusterId="cluster-1"
        mode={next}
        onClose={vi.fn()}
      />
    </QueryClientProvider>
  );
  const view = render(tree(mode));
  return {
    ...view,
    client,
    rerenderMode: (next: Mode) => view.rerender(tree(next)),
  };
}

describe("AppInstallModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    catalogVersions.data.data = [catalogVersions.data.data[0]];
    vi.mocked(getClusterAppValues).mockResolvedValue(
      "retained: release-settings\n",
    );
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
  it("preserves edited values when chart name and same-version defaults rerender", async () => {
    const view = renderModal();
    await screen.findByRole("button", { name: "Form" });
    fireEvent.click(screen.getByRole("button", { name: "YAML" }));
    const editor = screen.getByRole("textbox", { name: "Values (YAML)" });
    fireEvent.change(editor, { target: { value: "custom: keep-me\n" } });
    view.rerenderMode({ ...installMode, chartName: "renamed-chart" });
    await act(async () => {
      view.client.setQueryData(
        queryKeys.catalog.installChartValues("project-1", "chart-1", "91.5.2"),
        {
          chart: "renamed-chart",
          version: "91.5.2",
          defaultValues: "custom: replace-me\n",
        },
      );
    });
    expect(editor).toHaveValue("custom: keep-me\n");
  });

  it("hydrates new-version defaults only in install mode", async () => {
    catalogVersions.data.data.push({
      ...catalogVersions.data.data[0],
      id: "version-2",
      version: "92.0.0",
    });
    const view = renderModal();
    await screen.findByRole("button", { name: "Form" });
    fireEvent.click(screen.getByRole("button", { name: "YAML" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Values (YAML)" }), {
      target: { value: "custom: old-version\n" },
    });
    view.client.setQueryData(
      queryKeys.catalog.installChartValues("project-1", "chart-1", "92.0.0"),
      {
        chart: "kube-prometheus-stack",
        version: "92.0.0",
        defaultValues: "newVersion: true\n",
      },
    );
    fireEvent.change(screen.getByRole("combobox", { name: "Version" }), {
      target: { value: "version-2" },
    });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("textbox", {
            name: "Values (YAML)",
          }) as HTMLTextAreaElement
        ).value,
      ).toContain("newVersion: true"),
    );
    expect(
      (
        screen.getByRole("textbox", {
          name: "Values (YAML)",
        }) as HTMLTextAreaElement
      ).value,
    ).not.toContain("old-version");
  });

  it("retains saved upgrade values and operator edits across defaults and version changes", async () => {
    catalogVersions.data.data.push({
      ...catalogVersions.data.data[0],
      id: "version-2",
      version: "92.0.0",
    });
    const mode: Mode = {
      kind: "upgrade",
      installedChartId: "installed-1",
      chartId: "chart-1",
      chartName: "kube-prometheus-stack",
      currentVersionId: "version-1",
      currentValues: "initial: value\n",
      releaseName: "monitoring",
      namespace: "monitoring",
    };
    const view = renderModal(mode);
    await screen.findByRole("button", { name: "Form" });
    fireEvent.click(screen.getByRole("button", { name: "YAML" }));
    await waitFor(() =>
      expect(
        screen.getByRole("textbox", { name: "Values (YAML)" }),
      ).toHaveValue("retained: release-settings\n"),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Values (YAML)" }), {
      target: { value: "retained: operator-edit\n" },
    });
    fireEvent.change(screen.getByRole("combobox", { name: "Version" }), {
      target: { value: "version-2" },
    });
    view.rerenderMode({ ...mode, chartName: "renamed-chart" });
    await waitFor(() =>
      expect(getChartDefaultValues).toHaveBeenCalledWith(
        "project-1",
        "chart-1",
        "92.0.0",
        expect.any(AbortSignal),
      ),
    );
    await waitFor(() => expect(view.client.isFetching()).toBe(0));
    expect(screen.getByRole("textbox", { name: "Values (YAML)" })).toHaveValue(
      "retained: operator-edit\n",
    );
  });
});
