import { beforeEach, describe, expect, it, vi } from "vitest";
import * as generated from "@/lib/api/generated/client";
import {
  createClusterRoleBinding,
  createGlobalRole,
  getGlobalRoles,
  getMyEffectivePermissions,
} from "./rbac";

vi.mock("@/lib/api/generated/client", () => ({
  deleteRbacClusterRoleBindingsById: vi.fn(),
  deleteRbacGlobalRoleBindingsById: vi.fn(),
  deleteRbacProjectRoleBindingsById: vi.fn(),
  getRbacClusterRoleBindings: vi.fn(),
  getRbacClusterRoles: vi.fn(),
  getRbacEffectivePermissionsByUserId: vi.fn(),
  getRbacGlobalRoleBindings: vi.fn(),
  getRbacGlobalRoles: vi.fn(),
  getRbacMyPermissions: vi.fn(),
  getRbacProjectRoleBindings: vi.fn(),
  getRbacProjectRoles: vi.fn(),
  postRbacClusterRoleBindings: vi.fn(),
  postRbacClusterRoles: vi.fn(),
  postRbacGlobalRoleBindings: vi.fn(),
  postRbacGlobalRoles: vi.fn(),
  postRbacPermissionPreview: vi.fn(),
  postRbacProjectRoleBindings: vi.fn(),
  postRbacProjectRoles: vi.fn(),
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
    vi.mocked(generated.getRbacGlobalRoles).mockResolvedValueOnce({
      data: [roleWire],
      count: 1,
      next: null,
      previous: null,
    });

    await expect(getGlobalRoles()).resolves.toEqual([
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

  it("maps the server's effective-permission provenance contract", async () => {
    vi.mocked(generated.getRbacMyPermissions).mockResolvedValueOnce({
      data: {
        subject: { user_id: "9fa85f64-5717-4562-b3fc-2c963f66afa6", self: true },
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
