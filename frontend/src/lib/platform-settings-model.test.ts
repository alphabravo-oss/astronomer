import { describe, expect, it } from "vitest";
import {
  PLATFORM_SETTINGS_DEFAULTS,
  diffPlatformSettings,
  hydratePlatformSettings,
} from "./platform-settings-model";

describe("platform settings form model", () => {
  it("hydrates the backend's canonical banner and minute-based token keys", () => {
    const hydrated = hydratePlatformSettings([
      { key: "banner.login_text", value: "Authorized users only" },
      { key: "banner.global_color", value: "critical" },
      { key: "token.default_ttl_min", value: 90 },
      { key: "token.max_ttl_min", value: 1440 },
    ]);
    expect(hydrated.banners).toMatchObject({
      loginBannerText: "Authorized users only",
      globalBannerColor: "critical",
    });
    expect(hydrated.tokens).toEqual({
      defaultTtlMinutes: 90,
      maxTtlMinutes: 1440,
    });
  });

  it("emits only accepted registry keys and never legacy plural/second keys", () => {
    const changed = structuredClone(PLATFORM_SETTINGS_DEFAULTS);
    changed.banners.globalBannerText = "Maintenance tonight";
    changed.tokens.defaultTtlMinutes = 120;
    expect(diffPlatformSettings(PLATFORM_SETTINGS_DEFAULTS, changed)).toEqual({
      "banner.global_text": "Maintenance tonight",
      "token.default_ttl_min": 120,
    });
  });
});
