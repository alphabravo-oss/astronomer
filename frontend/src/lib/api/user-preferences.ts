import {
  getAuthMePreferences,
  putAuthMePreferences,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";

export type UserPreferences =
  OpenAPIComponents["schemas"]["UserPreferences"];
export type ThemePreference = UserPreferences["theme"];
export type TableDensityPreference = UserPreferences["table_density"];
export type TimeFormatPreference = UserPreferences["time_format"];
export type FavoriteRoute = UserPreferences["favorites"][number];
export type LandingRoute = UserPreferences["landing_route"];

export const defaultUserPreferences: UserPreferences = {
  theme: "system",
  table_density: "comfortable",
  landing_route: "/dashboard",
  time_format: "locale",
  favorites: [],
};

export async function getUserPreferences(
  signal?: AbortSignal,
): Promise<UserPreferences> {
  const response = await getAuthMePreferences({ signal });
  return response.data;
}

export async function putUserPreferences(
  preferences: UserPreferences,
): Promise<UserPreferences> {
  const response = await putAuthMePreferences({ body: preferences });
  return response.data;
}

export const favoriteNavigationOptions: ReadonlyArray<{
  href: FavoriteRoute;
  label: string;
}> = [
  { href: "/dashboard", label: "Overview" },
  { href: "/dashboard/clusters", label: "Clusters" },
  { href: "/dashboard/projects", label: "Projects" },
  { href: "/dashboard/workloads", label: "Search: Workloads" },
  { href: "/dashboard/delivery", label: "Delivery" },
  { href: "/dashboard/monitoring", label: "Monitoring" },
  { href: "/dashboard/alerting", label: "Alerting" },
  { href: "/dashboard/logging", label: "Logging" },
  { href: "/dashboard/security", label: "Security" },
  { href: "/dashboard/rbac", label: "RBAC" },
  { href: "/dashboard/audit", label: "Audit Log" },
  { href: "/dashboard/tools", label: "Cluster Tools" },
  { href: "/dashboard/extensions", label: "Extensions" },
];

export const landingRouteOptions: ReadonlyArray<{
  value: LandingRoute;
  label: string;
}> = favoriteNavigationOptions.filter(
  (item): item is { href: LandingRoute; label: string } =>
    item.href !== "/dashboard/logging" &&
    item.href !== "/dashboard/rbac" &&
    item.href !== "/dashboard/tools" &&
    item.href !== "/dashboard/extensions",
).map(({ href, label }) => ({ value: href, label }));

export function isNavigableLandingRoute(route: string): route is LandingRoute {
  return route !== "/dashboard" && landingRouteOptions.some((option) => option.value === route);
}
