/**
 * Generated transport boundary for settings intentionally readable before
 * authentication. These operations preserve dotted and snake_case wire keys;
 * callers only receive a value after the standard response envelope is proven.
 */
import {
  getSettingsBanner,
  getSettingsBranding,
  getSettingsRegistration,
  getSettingsSsoPresets,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Contracts = OpenAPIComponents["schemas"];

export type PublicBannerConfig = Contracts["PublicBannerSettings"];
export type PublicBrandingConfig = Contracts["PublicBrandingSettings"];
export type PublicRegistrationConfig =
  Contracts["PublicRegistrationSettings"];
export type RegistrationTLSMode =
  PublicRegistrationConfig["registration.tls_mode"];
export type SSOPresetDefinition = Contracts["SSOPreset"];

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

export async function getPublicBranding(): Promise<PublicBrandingConfig> {
  return requireData(await getSettingsBranding(), "getPublicBranding");
}

export async function getPublicBanner(): Promise<PublicBannerConfig> {
  return requireData(await getSettingsBanner(), "getPublicBanner");
}

export async function getRegistrationTLS(): Promise<{
  mode: RegistrationTLSMode;
  caBundle: string;
}> {
  const settings = requireData(
    await getSettingsRegistration(),
    "getRegistrationTLS",
  );
  return {
    mode: settings["registration.tls_mode"],
    caBundle: settings["registration.ca_bundle"],
  };
}

export async function getSSOPresets(): Promise<SSOPresetDefinition[]> {
  return requireData(await getSettingsSsoPresets(), "getSSOPresets");
}
