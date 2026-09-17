import type { BrowserContext, Page } from "@playwright/test";

// Shared e2e auth seed: the session/CSRF cookies the middleware checks plus
// the persisted `astronomer-auth` envelope the auth store hydrates
// from. The localStorage shape is load-bearing (see D21): keep it
// byte-compatible with the store's persist config.

export const SESSION_COOKIE = "astronomer_session";
export const CSRF_COOKIE = "astronomer_csrf";

type UserPreferencesFixture = {
  theme: "system" | "light" | "dark";
  table_density: "comfortable" | "compact";
  landing_route: string;
  time_format: "locale" | "12h" | "24h";
  favorites: string[];
};

const DEFAULT_USER_PREFERENCES: UserPreferencesFixture = {
  // Keep shell tests deterministic. A system preference makes the server
  // response race the browser colour-scheme and can remount form controls
  // while a test is interacting with them.
  theme: "light",
  table_density: "comfortable",
  landing_route: "/dashboard",
  time_format: "locale",
  favorites: [],
};

type SeedAuthOptions = {
  preferences?: Partial<UserPreferencesFixture>;
  preserveStorageState?: boolean;
};

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
  options: SeedAuthOptions = {},
) {
  const preferences = {
    ...DEFAULT_USER_PREFERENCES,
    ...options.preferences,
  };
  // Preferences are part of the authenticated application shell, just like
  // /auth/me. Register the exact endpoint after each spec's broad API route so
  // Playwright's last-registered-first matching cannot let a catch-all return
  // an unrelated array and crash every dashboard route.
  await page.route("**/api/v1/auth/me/preferences**", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        json: { status: 200, data: preferences },
      });
      return;
    }
    await route.fulfill({
      json: {
        status: 200,
        data: {
          ...preferences,
          ...(await route.request().postDataJSON()),
        },
      },
    });
  });
  // The namespace scope is a shell-level authorization invariant. Specs that
  // exercise a leaf route should not each have to rediscover and duplicate
  // these two supporting contracts.
  await page.route("**/api/v1/rbac/my-permissions/**", async (route) => {
    const record = user as Record<string, unknown>;
    await route.fulfill({
      json: {
        data: {
          subject: { user_id: String(record.id ?? "e2e-user"), self: true },
          superuser: Boolean(record.is_superuser ?? record.isSuperuser),
          context: {
            namespace_scoped_bindings_supported: true,
            warnings: [],
          },
          bindings: [],
          permissions: [],
        },
      },
    });
  });
  await page.route("**/api/v1/clusters/*/namespaces/**", async (route) => {
    await route.fulfill({
      json: {
        data: [],
        pagination: {
          limit: 200,
          offset: 0,
          total: 0,
          has_more: false,
          next_offset: null,
        },
      },
    });
  });
  if (options.preserveStorageState) return;
  await context.addCookies([
    {
      name: SESSION_COOKIE,
      value: "e2e-session",
      domain: "127.0.0.1",
      path: "/",
    },
    { name: CSRF_COOKIE, value: "e2e-csrf", domain: "127.0.0.1", path: "/" },
  ]);
  await page.addInitScript(
    ({ storedUser, storedTheme }) => {
      window.localStorage.setItem(
        "astronomer-auth",
        JSON.stringify({
          state: { user: storedUser, isAuthenticated: true },
          version: 2,
        }),
      );
      // Match the server-owned preference from first paint. This prevents a
      // light/system flash from invalidating accessibility and visual results
      // before the preferences query settles.
      window.localStorage.setItem("astronomer-theme", storedTheme);
    },
    { storedUser: user, storedTheme: preferences.theme },
  );
}
