import { beforeEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import {
  getToolConfiguration,
  getTools,
  previewToolInstall,
  uninstallTool,
} from "@/lib/api/tools";

vi.mock("@/lib/api/generated/client", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("@/lib/api/generated/client")>();
  return {
    ...actual,
    deleteToolsBySlugUninstall: vi.fn(),
    getTools: vi.fn(),
    getToolsBySlugConfiguration: vi.fn(),
    postToolsBySlugPreview: vi.fn(),
  };
});

const toolWire = {
  id: "1fa85f64-5717-4562-b3fc-2c963f66afa6",
  slug: "fluent-bit",
  name: "Fluent Bit",
  description: "Log forwarder",
  icon: "file-text",
  category: "observability",
  charts: [
    {
      chart_name: "fluent-bit",
      repo_url: "https://fluent.github.io/helm-charts",
      namespace: "astronomer-logging",
      order: 0,
    },
  ],
  version_constraint: "1.2.3",
  default_namespace: "astronomer-logging",
  is_builtin: true,
  is_enabled: true,
  helm_chart_id: null,
  presets: { default: {} },
  service_name: "",
  service_port: null,
  service_path: "/",
  sub_services: [],
  form_schema: {
    fields: [
      {
        path: "storage.enabled",
        label: "Storage",
        type: "storage" as const,
        group: "Storage",
        storage_class_path: "storage.className",
      },
    ],
  },
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:00:00Z",
};

const operationWire = {
  id: "2fa85f64-5717-4562-b3fc-2c963f66afa6",
  targetType: "tool_installation",
  targetKey: "cluster/fluent-bit",
  operationType: "uninstall" as const,
  status: "pending" as const,
  attemptCount: 0,
  startedAt: null,
  completedAt: null,
  errorMessage: "",
  createdAt: "2026-08-23T00:00:00Z",
  updatedAt: "2026-08-23T00:00:00Z",
};

const UUID_V4 =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

describe("tools generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps the raw tool definition into one camelCase view model", async () => {
    vi.mocked(generated.getTools).mockResolvedValueOnce({
      data: [toolWire],
      pagination: {
        total: 1,
        limit: 100,
        offset: 0,
        has_more: false,
        next_offset: null,
      },
    });

    const [tool] = await getTools();
    expect(tool).toEqual(
      expect.objectContaining({
        slug: "fluent-bit",
        defaultNamespace: "astronomer-logging",
        versionConstraint: "1.2.3",
        isBuiltin: true,
      }),
    );
    expect(tool.charts[0]).toEqual(
      expect.objectContaining({
        chartName: "fluent-bit",
        repoUrl: toolWire.charts[0].repo_url,
      }),
    );
    expect(tool.formSchema?.fields[0].storageClassPath).toBe(
      "storage.className",
    );
  });

  it("maps preview chart keys explicitly", async () => {
    vi.mocked(generated.postToolsBySlugPreview).mockResolvedValueOnce({
      data: {
        charts: [
          {
            chart_name: "fluent-bit",
            chart_version: "1.2.3",
            namespace: "astronomer-logging",
            values_yaml: "replicas: 2",
          },
        ],
        preset: "production",
        checks: [],
      },
    });

    await expect(
      previewToolInstall("fluent-bit", {
        cluster_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
        preset: "production",
        values_override: "replicas: 2",
      }),
    ).resolves.toEqual({
      charts: [
        {
          chartName: "fluent-bit",
          chartVersion: "1.2.3",
          namespace: "astronomer-logging",
          valuesYaml: "replicas: 2",
        },
      ],
      preset: "production",
      checks: [],
    });
    expect(generated.postToolsBySlugPreview).toHaveBeenCalledWith({
      path: { slug: "fluent-bit" },
      body: {
        cluster_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
        preset: "production",
        values_override: "replicas: 2",
      },
    });
  });

  it("maps the saved multi-release configuration", async () => {
    vi.mocked(generated.getToolsBySlugConfiguration).mockResolvedValueOnce({
      data: {
        preset: "development",
        values_yaml: "istiod:\n  replicaCount: 1\n",
        releases: [
          {
            id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
            release_name: "istiod",
            namespace: "istio-system",
            revision: 3,
          },
        ],
      },
    });

    await expect(
      getToolConfiguration("istio", "4fa85f64-5717-4562-b3fc-2c963f66afa6"),
    ).resolves.toEqual({
      preset: "development",
      valuesYaml: "istiod:\n  replicaCount: 1\n",
      releases: [
        {
          id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
          releaseName: "istiod",
          namespace: "istio-system",
          revision: 3,
        },
      ],
    });
  });

  it("preserves the required cluster body on DELETE uninstall", async () => {
    vi.mocked(generated.deleteToolsBySlugUninstall).mockResolvedValueOnce({
      data: operationWire,
    });
    const clusterId = "3fa85f64-5717-4562-b3fc-2c963f66afa6";

    await uninstallTool("fluent-bit", { cluster_id: clusterId });

    expect(generated.deleteToolsBySlugUninstall).toHaveBeenCalledWith({
      path: { slug: "fluent-bit" },
      headerParams: { "Idempotency-Key": expect.stringMatching(UUID_V4) },
      body: { cluster_id: clusterId },
    });
  });

  it("sends explicit persistent-data confirmation on destructive uninstall", async () => {
    vi.mocked(generated.deleteToolsBySlugUninstall).mockResolvedValueOnce({
      data: operationWire,
    });
    const clusterId = "3fa85f64-5717-4562-b3fc-2c963f66afa6";

    await uninstallTool("longhorn", {
      cluster_id: clusterId,
      confirm_data_deletion: true,
    });

    expect(generated.deleteToolsBySlugUninstall).toHaveBeenCalledWith({
      path: { slug: "longhorn" },
      headerParams: { "Idempotency-Key": expect.stringMatching(UUID_V4) },
      body: {
        cluster_id: clusterId,
        confirm_data_deletion: true,
      },
    });
  });
});
