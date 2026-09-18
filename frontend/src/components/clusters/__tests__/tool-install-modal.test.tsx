import { fireEvent, render, screen, within } from "@testing-library/react";
import * as yaml from "js-yaml";
import { ToolInstallModal } from "@/components/clusters/tool-install-modal";
import type { ClusterTool } from "@/types";

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    isLoading: false,
    data: {
      charts: [
        {
          chartName: "base",
          releaseName: "istio-base",
          namespace: "istio-system",
          chartVersion: "1.31.0",
          valuesYaml: "defaultRevision: default\n",
        },
        {
          chartName: "istiod",
          releaseName: "istiod",
          namespace: "istio-system",
          chartVersion: "1.31.0",
          valuesYaml: "replicaCount: 1\nautoscaleEnabled: false\n",
        },
      ],
    },
  }),
}));

const tool: ClusterTool = {
  id: "istio",
  slug: "istio",
  name: "Istio",
  description: "Service mesh",
  icon: "network",
  category: "mesh",
  versionConstraint: "1.31.0",
  defaultNamespace: "istio-system",
  isBuiltin: true,
  isEnabled: true,
  charts: [
    {
      chartName: "base",
      repoUrl: "https://example.com",
      namespace: "istio-system",
      order: 0,
      releaseName: "istio-base",
      valuesKey: "base",
    },
    {
      chartName: "istiod",
      repoUrl: "https://example.com",
      namespace: "istio-system",
      order: 1,
      releaseName: "istiod",
      valuesKey: "istiod",
    },
  ],
  presets: { default: {}, development: {} },
  serviceName: "",
  servicePort: null,
  servicePath: "",
  subServices: [],
  createdAt: "2026-09-10",
  updatedAt: "2026-09-10",
  formSchema: {
    fields: [
      {
        path: "istiod.replicaCount",
        label: "Replicas",
        type: "number",
        group: "Control plane",
        default: "2",
      },
      {
        path: "istiod.autoscaleEnabled",
        label: "Autoscale",
        type: "boolean",
        group: "Control plane",
        default: "true",
      },
    ],
  },
};

describe("ToolInstallModal release plans", () => {
  it("shows the complete ordered plan and the selected preset without overriding it", () => {
    const confirm = vi.fn();
    render(
      <ToolInstallModal
        tool={tool}
        clusterId="cluster"
        preset="development"
        onConfirm={confirm}
        onClose={vi.fn()}
      />,
    );
    expect(
      within(
        screen.getByRole("list", { name: "Release installation order" }),
      ).getAllByRole("listitem"),
    ).toHaveLength(2);
    expect(screen.getByRole("spinbutton", { name: "Replicas" })).toHaveValue(1);
    expect(
      screen.getByRole("checkbox", { name: "Autoscale" }),
    ).not.toBeChecked();
    expect(screen.getByRole("combobox", { name: "Preset" })).toHaveValue(
      "development",
    );
    fireEvent.click(screen.getByRole("button", { name: "Install" }));
    expect(confirm).toHaveBeenCalledWith(undefined, "development");
  });
  it("sends only explicit edits under the release values key", () => {
    const confirm = vi.fn();
    render(
      <ToolInstallModal
        tool={tool}
        clusterId="cluster"
        preset="development"
        onConfirm={confirm}
        onClose={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByRole("spinbutton", { name: "Replicas" }), {
      target: { value: "3" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Install" }));
    expect(yaml.load(confirm.mock.calls[0][0])).toEqual({
      istiod: { replicaCount: 3 },
    });
  });
});
