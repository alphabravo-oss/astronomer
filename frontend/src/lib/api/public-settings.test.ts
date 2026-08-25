import { beforeEach, describe, expect, it, vi } from "vitest";

const { request } = vi.hoisted(() => ({ request: vi.fn() }));
vi.mock("@/lib/api/transport", () => ({ default: { request } }));

import {
  getPublicBanner,
  getPublicBranding,
  getRegistrationTLS,
  getSSOPresets,
} from "./public-settings";

const BRANDING = {
  "branding.product_name": "Astronomer Enterprise",
  "branding.logo_url": "https://assets.example.test/logo.svg",
  "branding.primary_color": "#123ABC",
  "branding.support_url": "https://support.example.test",
  "branding.copyright": "Example Corp",
} as const;

const BANNER = {
  "banner.login_text": "Authorized users only",
  "banner.global_text": "Maintenance at 22:00 UTC",
  "banner.global_color": "warning",
} as const;

const REGISTRATION = {
  "registration.tls_mode": "private_ca",
  "registration.ca_bundle": "-----BEGIN CERTIFICATE-----\nTEST\n",
} as const;

const PRESETS = [
  {
    key: "azure-ad",
    display_name: "Microsoft Entra ID",
    type: "oidc",
    issuer_url_template: "https://login.microsoftonline.com/{tenant}/v2.0",
    default_scopes: ["openid", "email", "profile"],
    callback_path_hint: "/auth/login/azure-ad/callback",
    required_fields: [
      {
        name: "tenant",
        label: "Directory (tenant) ID",
        placeholder: "tenant-id",
        kind: "tenant",
        required: true,
      },
    ],
    docs_url: "https://learn.microsoft.com/entra/identity-platform/",
    logo_slug: "azure",
  },
] as const;

function lastRequest() {
  return request.mock.calls.at(-1)?.[0];
}

beforeEach(() => {
  request.mockReset();
  request.mockImplementation(async (config) => {
    switch (config.url) {
      case "/api/v1/settings/branding":
        return { data: { data: BRANDING } };
      case "/api/v1/settings/banner":
        return { data: { data: BANNER } };
      case "/api/v1/settings/registration":
        return { data: { data: REGISTRATION } };
      case "/api/v1/settings/sso/presets":
        return { data: { data: PRESETS } };
      default:
        throw new Error(`Unexpected request: ${String(config.url)}`);
    }
  });
});

describe("generated public settings boundary", () => {
  it("preserves exact dotted settings keys on the raw wire", async () => {
    await expect(getPublicBranding()).resolves.toEqual(BRANDING);
    await expect(getPublicBanner()).resolves.toEqual(BANNER);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        baseURL: "",
        method: "GET",
        url: "/api/v1/settings/banner",
        responseType: "json",
      }),
    );
  });

  it("unwraps the registration envelope instead of fabricating defaults", async () => {
    await expect(getRegistrationTLS()).resolves.toEqual({
      mode: "private_ca",
      caBundle: REGISTRATION["registration.ca_bundle"],
    });
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/settings/registration",
      }),
    );

    request.mockResolvedValueOnce({ data: {} });
    await expect(getRegistrationTLS()).rejects.toThrow(
      "getRegistrationTLS returned no data payload",
    );
  });

  it("keeps the documented SSO snake_case DTO intact", async () => {
    await expect(getSSOPresets()).resolves.toEqual(PRESETS);
    expect(lastRequest()).toEqual(
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/settings/sso/presets",
      }),
    );
  });
});
