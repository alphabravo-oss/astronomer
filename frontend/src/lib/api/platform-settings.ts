/** Generated platform-settings API boundary and operator form view model. */
import {
  deleteAdminSettingsByKey,
  getAdminSettings,
  getAdminSettingsByKey,
  putAdminSettings,
  putAdminSettingsByKey,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Contracts = OpenAPIComponents["schemas"];
type PlatformSettingWire = Contracts["PlatformSetting"];

export interface PlatformSettingView {
  key: string;
  value: unknown;
  description: string;
  type: PlatformSettingWire["type"];
  defaultValue: unknown;
  updatedBy?: string;
  updatedAt?: string;
  isDefault: boolean;
}

export interface PlatformSettingsGrouped {
  branding: {
    logoUrl: string;
    productName: string;
    primaryColor: string;
    supportUrl: string;
    copyright: string;
  };
  banners: {
    loginBannerText: string;
    globalBannerText: string;
    globalBannerColor: "info" | "warning" | "critical";
  };
  features: {
    catalog: boolean;
    projects: boolean;
    monitoring: boolean;
    security: boolean;
    backups: boolean;
    extensions: boolean;
  };
  tokens: {
    defaultTtlMinutes: number;
    maxTtlMinutes: number;
  };
  session: { timeoutMinutes: number };
  telemetry: { enabled: boolean; endpoint: string };
  registration: {
    tlsMode: "public_ca" | "private_ca" | "insecure";
    caBundle: string;
  };
}

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

export function mapPlatformSetting(
  wire: PlatformSettingWire,
): PlatformSettingView {
  return {
    key: wire.key,
    value: wire.value,
    description: wire.description,
    type: wire.type,
    defaultValue: wire.default,
    updatedBy: wire.updated_by,
    updatedAt: wire.updated_at,
    isDefault: wire.is_default,
  };
}

export async function listPlatformSettings(): Promise<PlatformSettingView[]> {
  const envelope = await getAdminSettings();
  return requireData(envelope, "listPlatformSettings").map(mapPlatformSetting);
}

export async function getPlatformSetting(
  key: string,
): Promise<PlatformSettingView> {
  return mapPlatformSetting(
    requireData(
      await getAdminSettingsByKey({ path: { key } }),
      "getPlatformSetting",
    ),
  );
}

export async function putPlatformSetting(
  key: string,
  value: unknown,
): Promise<PlatformSettingView> {
  return mapPlatformSetting(
    requireData(
      await putAdminSettingsByKey({ path: { key }, body: { value } }),
      "putPlatformSetting",
    ),
  );
}

export async function deletePlatformSetting(
  key: string,
): Promise<PlatformSettingView> {
  return mapPlatformSetting(
    requireData(
      await deleteAdminSettingsByKey({ path: { key } }),
      "deletePlatformSetting",
    ),
  );
}

export async function savePlatformSettingsBatch(
  updates: Record<string, unknown>,
): Promise<PlatformSettingView[]> {
  const envelope = await putAdminSettings({ body: { updates } });
  return requireData(envelope, "savePlatformSettingsBatch").map(
    mapPlatformSetting,
  );
}
