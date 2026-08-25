import { beforeEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import {
  assignSecurityPolicy,
  createPodSecurityTemplate,
  getClusterSecurityPolicies,
  getPodSecurityTemplates,
} from "./security-policies";

vi.mock("@/lib/api/generated/client", () => ({
  deleteSecurityPoliciesById: vi.fn(),
  deleteSecurityTemplatesById: vi.fn(),
  getSecurityPolicies: vi.fn(),
  getSecurityTemplates: vi.fn(),
  postSecurityPolicies: vi.fn(),
  postSecurityPoliciesByIdApply: vi.fn(),
  postSecurityTemplates: vi.fn(),
  putSecurityTemplatesById: vi.fn(),
}));

const templateWire = {
  id: "1fa85f64-5717-4562-b3fc-2c963f66afa6",
  name: "restricted-production",
  description: "Production defaults",
  is_default: true,
  is_builtin: false,
  enforce_level: "restricted" as const,
  enforce_version: "latest",
  audit_level: "restricted" as const,
  audit_version: "latest",
  warn_level: "restricted" as const,
  warn_version: "latest",
  exempt_usernames: [],
  exempt_runtime_classes: [],
  exempt_namespaces: ["kube-system"],
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:01:00Z",
};

const policyWire = {
  id: "2fa85f64-5717-4562-b3fc-2c963f66afa6",
  cluster_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  template_id: templateWire.id,
  applied_at: null,
  sync_status: "pending" as const,
  error_message: "",
  created_at: "2026-08-23T00:02:00Z",
  updated_at: "2026-08-23T00:02:00Z",
};

describe("security policy generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps PSA templates from raw wire casing", async () => {
    vi.mocked(generated.getSecurityTemplates).mockResolvedValueOnce({
      data: [templateWire],
      count: 1,
      next: null,
      previous: null,
    });

    await expect(getPodSecurityTemplates()).resolves.toEqual([
      expect.objectContaining({
        id: templateWire.id,
        isDefault: true,
        enforceLevel: "restricted",
        exemptNamespaces: ["kube-system"],
      }),
    ]);
    expect(generated.getSecurityTemplates).toHaveBeenCalledWith({
      query: { limit: 200 },
    });
  });

  it("serializes the complete PSA request explicitly", async () => {
    vi.mocked(generated.postSecurityTemplates).mockResolvedValueOnce({
      data: templateWire,
    });

    await createPodSecurityTemplate({
      name: "restricted-production",
      description: "Production defaults",
      enforceLevel: "restricted",
      enforceVersion: "latest",
      auditLevel: "restricted",
      auditVersion: "latest",
      warnLevel: "restricted",
      warnVersion: "latest",
      exemptNamespaces: ["kube-system"],
      exemptRuntimeClasses: [],
      exemptUsernames: [],
    });

    expect(generated.postSecurityTemplates).toHaveBeenCalledWith({
      body: expect.objectContaining({
        name: "restricted-production",
        enforce_level: "restricted",
        exempt_namespaces: ["kube-system"],
      }),
    });
  });

  it("keeps policy identity honest instead of inventing joined names and levels", async () => {
    vi.mocked(generated.getSecurityPolicies).mockResolvedValueOnce({
      data: [policyWire],
      count: 1,
      next: null,
      previous: null,
    });

    const [policy] = await getClusterSecurityPolicies();
    expect(policy).toEqual({
      id: policyWire.id,
      clusterId: policyWire.cluster_id,
      templateId: policyWire.template_id,
      appliedAt: null,
      syncStatus: "pending",
      errorMessage: "",
      createdAt: policyWire.created_at,
      updatedAt: policyWire.updated_at,
    });
    expect(policy).not.toHaveProperty("clusterName");
    expect(policy).not.toHaveProperty("enforceLevel");
  });

  it("assigns through the typed generated request", async () => {
    vi.mocked(generated.postSecurityPolicies).mockResolvedValueOnce({
      data: policyWire,
    });
    await assignSecurityPolicy({
      cluster_id: policyWire.cluster_id,
      template_id: policyWire.template_id,
    });
    expect(generated.postSecurityPolicies).toHaveBeenCalledWith({
      body: {
        cluster_id: policyWire.cluster_id,
        template_id: policyWire.template_id,
      },
    });
  });
});
