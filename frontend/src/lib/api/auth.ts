import {
  getAuthMe,
  postAuthChangePassword,
  postStreamsTickets,
} from "@/lib/api/generated/client";
import type { User, UserRoleBinding } from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Schemas = OpenAPIComponents["schemas"];
type UserWire = Schemas["User"];
type RoleBindingWire = Schemas["AuthUserRoleBinding"];

export type StreamTicketType = Schemas["StreamTicketRequest"]["stream_type"];

function requiredString(value: string | undefined, field: string): string {
  if (!value) throw new Error(`Auth API response omitted ${field}`);
  return value;
}

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

function mapRoleBinding(wire: RoleBindingWire): UserRoleBinding {
  return {
    id: wire.id,
    roleId: wire.role_id,
    roleName: wire.role_name,
    roleRules: wire.role_rules.map((rule) => ({
      resource: rule.resource,
      resources: rule.resources,
      verbs: rule.verbs,
      apiGroups: rule.api_groups,
    })),
    group: wire.group,
    clusterId: wire.cluster_id,
    projectId: wire.project_id,
  };
}

/** Exact auth/me wire-to-session mapping; authorization never depends on interceptor casing. */
export function mapCurrentUser(wire: UserWire): User {
  const username = requiredString(wire.username, "user.username");
  const displayName = [wire.first_name, wire.last_name]
    .filter(Boolean)
    .join(" ")
    .trim();
  const roles = {
    global: (wire.roles?.global ?? []).map(mapRoleBinding),
    cluster: (wire.roles?.cluster ?? []).map(mapRoleBinding),
    project: (wire.roles?.project ?? []).map(mapRoleBinding),
  };
  const isSuperuser = wire.is_superuser ?? false;
  const mustChangePassword = wire.must_change_password ?? false;
  return {
    id: requiredString(wire.id, "user.id"),
    username,
    email: requiredString(wire.email, "user.email"),
    displayName: displayName || username,
    provider: "local",
    globalRoles: roles.global
      .map((binding) => binding.roleName ?? binding.roleId ?? "")
      .filter(Boolean),
    isSuperuser,
    is_superuser: isSuperuser,
    roles,
    enabled: wire.is_active ?? false,
    lastLogin: wire.last_login ?? "",
    createdAt: wire.date_joined ?? "",
    mustChangePassword,
    must_change_password: mustChangePassword,
  };
}

export async function getCurrentUser(): Promise<User> {
  return mapCurrentUser(requireData(await getAuthMe(), "getCurrentUser"));
}

export async function changeOwnPassword(
  currentPassword: string,
  newPassword: string,
): Promise<{ detail?: string; must_change_password?: boolean }> {
  return postAuthChangePassword({
    body: {
      current_password: currentPassword,
      new_password: newPassword,
    },
  });
}

export async function createStreamTicket(
  streamType: StreamTicketType,
  clusterId?: string,
): Promise<{ ticket: string; expiresAt: string }> {
  const wire = requireData(
    await postStreamsTickets({
      body: { stream_type: streamType, cluster_id: clusterId },
    }),
    "createStreamTicket",
  );
  return {
    ticket: wire.ticket,
    expiresAt: wire.expires_at,
  };
}
