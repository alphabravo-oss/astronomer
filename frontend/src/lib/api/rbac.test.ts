import { beforeEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import {
  applyProjectRoleTemplate,
  createClusterRoleBinding,
  createGlobalRole,
  deleteRole,
  getGlobalRoles,
  getMyEffectivePermissions,
  listRoleTemplates,
  materializePrincipal,
  searchPrincipals,
  updateRole,
} from "./rbac";

vi.mock("@/lib/api/generated/client", () => ({
  deleteRbacClusterRolesById: vi.fn(),
  deleteRbacClusterRoleBindingsById: vi.fn(),
  deleteRbacGlobalRoleBindingsById: vi.fn(),
  deleteRbacProjectRoleBindingsById: vi.fn(),
  deleteRbacGlobalRolesById: vi.fn(),
  deleteRbacProjectRolesById: vi.fn(),
  getRbacClusterRoleBindings: vi.fn(),
  getRbacClusterRoles: vi.fn(),
  getRbacEffectivePermissionsByUserId: vi.fn(),
  getRbacGlobalRoleBindings: vi.fn(),
  getRbacGlobalRoles: vi.fn(),
  getRbacMyPermissions: vi.fn(),
  getRbacPrincipals: vi.fn(),
  getRbacProjectRoleBindings: vi.fn(),
  getRbacProjectRoles: vi.fn(),
  getRbacTemplates: vi.fn(),
  postRbacClusterRoleBindings: vi.fn(),
  postRbacClusterRoles: vi.fn(),
  postRbacGlobalRoleBindings: vi.fn(),
  postRbacGlobalRoles: vi.fn(),
  postRbacPermissionPreview: vi.fn(),
  postRbacPrincipalsMaterialize: vi.fn(),
  postRbacProjectRoleBindings: vi.fn(),
  postRbacProjectRoles: vi.fn(),
  postProjectsByIdApplyRbacTemplate: vi.fn(),
  putRbacClusterRolesById: vi.fn(),
  putRbacGlobalRolesById: vi.fn(),
  putRbacProjectRolesById: vi.fn(),
}));

const roleWire = {
  id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  name: "incident-responder",
  display_name: "Incident Responder",
  description: "Responds to production incidents",
  scope: "global" as const,
  is_builtin: false,
  rules: [
    {
      resource: "certificates",
      verbs: ["read", "list"],
      api_groups: ["cert-manager.io"],
    },
  ],
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:01:00Z",
};

describe("RBAC generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps role wire casing and uses bounded generated list pagination", async () => {
    const signal = new AbortController().signal;
    vi.mocked(generated.getRbacGlobalRoles).mockResolvedValueOnce({
      data: [roleWire],
      pagination: {
        total: 1,
        limit: 100,
        offset: 0,
        has_more: false,
        next_offset: null,
      },
    });

    await expect(getGlobalRoles(signal)).resolves.toEqual([
      expect.objectContaining({
        id: roleWire.id,
        displayName: "Incident Responder",
        isBuiltin: false,
        rules: [
          expect.objectContaining({
            resource: "certificates",
            apiGroups: ["cert-manager.io"],
          }),
        ],
      }),
    ]);
    expect(generated.getRbacGlobalRoles).toHaveBeenCalledWith({
      query: { limit: 200 },
      signal,
    });
  });

  it("creates roles through the snake_case generated request contract", async () => {
    vi.mocked(generated.postRbacGlobalRoles).mockResolvedValueOnce({
      data: roleWire,
    });

    await createGlobalRole({
      name: "incident-responder",
      displayName: "Incident Responder",
      description: "Responds to production incidents",
      rules: [
        {
          resource: "certificates",
          verbs: ["read"],
          apiGroups: ["cert-manager.io"],
        },
      ],
    });

    expect(generated.postRbacGlobalRoles).toHaveBeenCalledWith({
      body: {
        name: "incident-responder",
        display_name: "Incident Responder",
        description: "Responds to production incidents",
        rules: [
          {
            resource: "certificates",
            resources: undefined,
            verbs: ["read"],
            api_groups: ["cert-manager.io"],
          },
        ],
      },
    });
  });

  it("updates and deletes a scoped custom role through generated operations", async () => {
    vi.mocked(generated.putRbacProjectRolesById).mockResolvedValueOnce({
      data: roleWire,
    });

    await updateRole("project", roleWire.id, {
      name: roleWire.name,
      displayName: roleWire.display_name,
      rules: [{ resource: "workloads", verbs: ["read"] }],
    });
    expect(generated.putRbacProjectRolesById).toHaveBeenCalledWith(
      expect.objectContaining({ path: { id: roleWire.id } }),
    );

    await deleteRole("project", roleWire.id);
    expect(generated.deleteRbacProjectRolesById).toHaveBeenCalledWith({
      path: { id: roleWire.id },
    });
  });

  it("lists curated templates and applies one to a project user", async () => {
    vi.mocked(generated.getRbacTemplates).mockResolvedValueOnce({
      data: {
        count: 1,
        templates: [
          {
            name: "workload-viewer",
            display_name: "Workload Viewer",
            description: "Read project workloads",
            scope: "project",
            risk_level: "low",
            inherits: [],
            system_managed: true,
            category: "project",
            rules: [{ resource: "workloads", verbs: ["read"] }],
          },
        ],
      },
    });
    await expect(listRoleTemplates()).resolves.toEqual([
      expect.objectContaining({
        name: "workload-viewer",
        displayName: "Workload Viewer",
        scope: "project",
      }),
    ]);

    const projectId = "8fa85f64-5717-4562-b3fc-2c963f66afa6";
    const userId = "9fa85f64-5717-4562-b3fc-2c963f66afa6";
    vi.mocked(
      generated.postProjectsByIdApplyRbacTemplate,
    ).mockResolvedValueOnce({
      data: {
        id: "7fa85f64-5717-4562-b3fc-2c963f66afa6",
        role_id: roleWire.id,
        project_id: projectId,
        user_id: userId,
        group: "",
        created_at: "2026-09-10T00:00:00Z",
      },
    });
    await expect(
      applyProjectRoleTemplate({
        projectId,
        userId,
        templateName: "workload-viewer",
      }),
    ).resolves.toMatchObject({ scope: "project", projectId, userId });
    expect(generated.postProjectsByIdApplyRbacTemplate).toHaveBeenCalledWith({
      path: { id: projectId },
      body: { template_name: "workload-viewer", user_id: userId },
    });
  });

  it("maps namespace-scoped bindings without relying on response interceptors", async () => {
    vi.mocked(generated.postRbacClusterRoleBindings).mockResolvedValueOnce({
      data: {
        id: "7fa85f64-5717-4562-b3fc-2c963f66afa6",
        role_id: roleWire.id,
        cluster_id: "8fa85f64-5717-4562-b3fc-2c963f66afa6",
        namespace: "payments",
        user_id: "9fa85f64-5717-4562-b3fc-2c963f66afa6",
        group: "",
        created_at: "2026-08-23T00:02:00Z",
      },
    });

    await expect(
      createClusterRoleBinding({
        user_id: "9fa85f64-5717-4562-b3fc-2c963f66afa6",
        role_id: roleWire.id,
        cluster_id: "8fa85f64-5717-4562-b3fc-2c963f66afa6",
        namespace: "payments",
      }),
    ).resolves.toMatchObject({
      scope: "cluster",
      roleId: roleWire.id,
      clusterId: "8fa85f64-5717-4562-b3fc-2c963f66afa6",
      namespace: "payments",
    });
  });

  it("searches and materializes principals through generated contracts", async () => {
    vi.mocked(generated.getRbacPrincipals).mockResolvedValueOnce({
      data: {
        principals: [
          {
            kind: "external",
            connector_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
            connector_name: "employees",
            connector_type: "ldap",
            subject: "stable-42",
            email: "ada@example.com",
            username: "ada",
            display_name: "Ada Lovelace",
          },
        ],
        connectors: [],
      },
    });
    await expect(searchPrincipals("ada")).resolves.toMatchObject({
      principals: [{ kind: "external", subject: "stable-42" }],
    });
    expect(generated.getRbacPrincipals).toHaveBeenCalledWith({
      query: { q: "ada" },
      signal: undefined,
    });

    vi.mocked(generated.postRbacPrincipalsMaterialize).mockResolvedValueOnce({
      data: {
        id: "4fa85f64-5717-4562-b3fc-2c963f66afa6",
        user_id: "5fa85f64-5717-4562-b3fc-2c963f66afa6",
        connector_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
        kind: "pending",
        email: "ada@example.com",
        username: "ada",
        display_name: "Ada Lovelace",
      },
    });
    await materializePrincipal({
      connectorId: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
      subject: "stable-42",
    });
    expect(generated.postRbacPrincipalsMaterialize).toHaveBeenCalledWith({
      body: {
        connector_id: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
        subject: "stable-42",
      },
    });
  });

  it("maps the server's effective-permission provenance contract", async () => {
    vi.mocked(generated.getRbacMyPermissions).mockResolvedValueOnce({
      data: {
        subject: {
          user_id: "9fa85f64-5717-4562-b3fc-2c963f66afa6",
          self: true,
        },
        superuser: false,
        context: {
          cluster_id: "8fa85f64-5717-4562-b3fc-2c963f66afa6",
          namespace: "payments",
          namespace_scoped_bindings_supported: true,
          warnings: ["namespace context is enforced"],
        },
        bindings: [
          {
            scope: "cluster",
            binding_id: "7fa85f64-5717-4562-b3fc-2c963f66afa6",
            role_id: roleWire.id,
            cluster_id: "8fa85f64-5717-4562-b3fc-2c963f66afa6",
            namespace: "payments",
            rules: [{ resource: "pods", verbs: ["read"] }],
          },
        ],
        permissions: [
          {
            resource: "pods",
            verb: "read",
            applies_to_context: true,
            inherited: true,
            inherited_from: "viewer",
            sources: [
              {
                scope: "cluster",
                namespace: "payments",
                role_id: roleWire.id,
              },
            ],
          },
        ],
      },
    });

    await expect(
      getMyEffectivePermissions({
        clusterId: "8fa85f64-5717-4562-b3fc-2c963f66afa6",
        namespace: "payments",
      }),
    ).resolves.toMatchObject({
      subject: { self: true },
      context: {
        clusterId: "8fa85f64-5717-4562-b3fc-2c963f66afa6",
        namespace: "payments",
        namespaceScopedBindingsSupported: true,
      },
      permissions: [
        {
          resource: "pods",
          appliesToContext: true,
          inherited: true,
          inheritedFrom: "viewer",
          sources: [{ scope: "cluster", namespace: "payments" }],
        },
      ],
    });
    expect(generated.getRbacMyPermissions).toHaveBeenCalledWith({
      query: {
        cluster_id: "8fa85f64-5717-4562-b3fc-2c963f66afa6",
        project_id: undefined,
        namespace: "payments",
      },
    });
  });
});
