import { beforeEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import {
  getTools,
  previewToolInstall,
  uninstallTool,
} from "@/lib/api/tools";

vi.mock("@/lib/api/generated/client", async (importOriginal) => {
  const actual = await importOriginal<
    typeof import("@/lib/api/generated/client")
  >();
  return {
    ...actual,
    deleteToolsBySlugUninstall: vi.fn(),
    getTools: vi.fn(),
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
      count: 1,
      next: null,
      previous: null,
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
      expect.objectContaining({ chartName: "fluent-bit", repoUrl: toolWire.charts[0].repo_url }),
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
      },
    });

    await expect(
      previewToolInstall("fluent-bit", {
        cluster_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
        preset: "production",
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
});
