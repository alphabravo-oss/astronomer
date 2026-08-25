import type { PlatformSettingsGrouped } from "@/lib/api/platform-settings";

/** Registry defaults mirrored from the backend for pre-response form stability. */
export const PLATFORM_SETTINGS_DEFAULTS: PlatformSettingsGrouped = {
  branding: {
    logoUrl: "",
    productName: "Astronomer",
    primaryColor: "#0066CC",
    supportUrl: "",
    copyright: "",
  },
  banners: {
    loginBannerText: "",
    globalBannerText: "",
    globalBannerColor: "info",
  },
  features: {
    catalog: true,
    projects: true,
    monitoring: true,
    security: true,
    backups: true,
    extensions: false,
  },
  tokens: { defaultTtlMinutes: 60, maxTtlMinutes: 525600 },
  session: { timeoutMinutes: 60 },
  telemetry: {
    enabled: false,
    endpoint: "https://telemetry.alphabravo.io/astronomer",
  },
  registration: { tlsMode: "public_ca", caBundle: "" },
};

const PLATFORM_SETTING_KEYS: Record<
  string,
  (settings: PlatformSettingsGrouped) => unknown
> = {
  "branding.logo_url": (s) => s.branding.logoUrl,
  "branding.product_name": (s) => s.branding.productName,
  "branding.primary_color": (s) => s.branding.primaryColor,
  "branding.support_url": (s) => s.branding.supportUrl,
  "branding.copyright": (s) => s.branding.copyright,
  "banner.login_text": (s) => s.banners.loginBannerText,
  "banner.global_text": (s) => s.banners.globalBannerText,
  "banner.global_color": (s) => s.banners.globalBannerColor,
  "feature.catalog": (s) => s.features.catalog,
  "feature.projects": (s) => s.features.projects,
  "feature.monitoring": (s) => s.features.monitoring,
  "feature.security": (s) => s.features.security,
  "feature.backups": (s) => s.features.backups,
  "feature.extensions": (s) => s.features.extensions,
  "token.default_ttl_min": (s) => s.tokens.defaultTtlMinutes,
  "token.max_ttl_min": (s) => s.tokens.maxTtlMinutes,
  "session.timeout_minutes": (s) => s.session.timeoutMinutes,
  "telemetry.enabled": (s) => s.telemetry.enabled,
  "telemetry.endpoint": (s) => s.telemetry.endpoint,
  "registration.tls_mode": (s) => s.registration.tlsMode,
  "registration.ca_bundle": (s) => s.registration.caBundle,
};

export function hydratePlatformSettings(
  flat: Array<{ key: string; value: unknown }>,
): PlatformSettingsGrouped {
  const values = new Map(flat.map((setting) => [setting.key, setting.value]));
  const get = <T,>(key: string, fallback: T): T => {
    const value = values.get(key);
    return value === undefined || value === null ? fallback : (value as T);
  };
  const defaults = PLATFORM_SETTINGS_DEFAULTS;
  return {
    branding: {
      logoUrl: get("branding.logo_url", defaults.branding.logoUrl),
      productName: get("branding.product_name", defaults.branding.productName),
      primaryColor: get(
        "branding.primary_color",
        defaults.branding.primaryColor,
      ),
      supportUrl: get("branding.support_url", defaults.branding.supportUrl),
      copyright: get("branding.copyright", defaults.branding.copyright),
    },
    banners: {
      loginBannerText: get("banner.login_text", defaults.banners.loginBannerText),
      globalBannerText: get(
        "banner.global_text",
        defaults.banners.globalBannerText,
      ),
      globalBannerColor: get(
        "banner.global_color",
        defaults.banners.globalBannerColor,
      ),
    },
    features: {
      catalog: get("feature.catalog", defaults.features.catalog),
      projects: get("feature.projects", defaults.features.projects),
      monitoring: get("feature.monitoring", defaults.features.monitoring),
      security: get("feature.security", defaults.features.security),
      backups: get("feature.backups", defaults.features.backups),
      extensions: get("feature.extensions", defaults.features.extensions),
    },
    tokens: {
      defaultTtlMinutes: Number(
        get("token.default_ttl_min", defaults.tokens.defaultTtlMinutes),
      ),
      maxTtlMinutes: Number(
        get("token.max_ttl_min", defaults.tokens.maxTtlMinutes),
      ),
    },
    session: {
      timeoutMinutes: Number(
        get("session.timeout_minutes", defaults.session.timeoutMinutes),
      ),
    },
    telemetry: {
      enabled: get("telemetry.enabled", defaults.telemetry.enabled),
      endpoint: get("telemetry.endpoint", defaults.telemetry.endpoint),
    },
    registration: {
      tlsMode: get(
        "registration.tls_mode",
        defaults.registration.tlsMode,
      ) as PlatformSettingsGrouped["registration"]["tlsMode"],
      caBundle: get("registration.ca_bundle", defaults.registration.caBundle),
    },
  };
}

export function diffPlatformSettings(
  before: PlatformSettingsGrouped,
  after: PlatformSettingsGrouped,
): Record<string, unknown> {
  const updates: Record<string, unknown> = {};
  for (const [key, read] of Object.entries(PLATFORM_SETTING_KEYS)) {
    if (read(before) !== read(after)) updates[key] = read(after);
  }
  return updates;
}
