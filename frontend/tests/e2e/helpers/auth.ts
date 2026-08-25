import type { BrowserContext, Page } from "@playwright/test";

// Shared e2e auth seed: the session/CSRF cookies the middleware checks plus
// the persisted `astronomer-auth` envelope the auth store hydrates
// from. The localStorage shape is load-bearing (see D21): keep it
// byte-compatible with the store's persist config.

export const SESSION_COOKIE = "astronomer_session";
export const CSRF_COOKIE = "astronomer_csrf";

/** Convert the persisted camelCase session fixture into the exact /auth/me wire contract. */
export function authMeWire(input: unknown): Record<string, unknown> {
  const user = input as Record<string, unknown>;
  if ("first_name" in user) return user;
  const displayName =
    typeof user.displayName === "string" ? user.displayName.trim() : "";
  const [firstName = "", ...lastName] = displayName
    .split(/\s+/)
    .filter(Boolean);
  const sourceRoles = (user.roles ?? {}) as Record<string, unknown>;
  const roleArray = (scope: "global" | "cluster" | "project") =>
    (Array.isArray(sourceRoles[scope]) ? sourceRoles[scope] : []).map(
      (value) => {
        const binding = value as Record<string, unknown>;
        return {
          id: binding.id ?? "00000000-0000-0000-0000-000000000000",
          role_id:
            binding.role_id ??
            binding.roleId ??
            "00000000-0000-0000-0000-000000000000",
          role_name: binding.role_name ?? binding.roleName ?? "role",
          role_rules: binding.role_rules ?? binding.roleRules ?? [],
          group: binding.group ?? "",
          ...(scope === "cluster"
            ? { cluster_id: binding.cluster_id ?? binding.clusterId }
            : {}),
          ...(scope === "project"
            ? { project_id: binding.project_id ?? binding.projectId }
            : {}),
        };
      },
    );
  return {
    id: user.id,
    username: user.username,
    email: user.email,
    first_name: firstName,
    last_name: lastName.join(" "),
    is_active: user.enabled ?? true,
    is_staff: user.isStaff ?? false,
    is_superuser: user.is_superuser ?? user.isSuperuser ?? false,
    date_joined: user.createdAt ?? new Date(0).toISOString(),
    last_login: user.lastLogin ?? null,
    must_change_password:
      user.must_change_password ?? user.mustChangePassword ?? false,
    roles: {
      global: roleArray("global"),
      cluster: roleArray("cluster"),
      project: roleArray("project"),
    },
  };
}

export async function seedAuth(
  context: BrowserContext,
  page: Page,
  user: unknown,
) {
  await context.addCookies([
    {
      name: SESSION_COOKIE,
      value: "e2e-session",
      domain: "127.0.0.1",
      path: "/",
    },
    { name: CSRF_COOKIE, value: "e2e-csrf", domain: "127.0.0.1", path: "/" },
  ]);
  await page.addInitScript((storedUser) => {
    window.localStorage.setItem(
      "astronomer-auth",
      JSON.stringify({
        state: { user: storedUser, isAuthenticated: true },
        version: 2,
      }),
    );
  }, user);
}
