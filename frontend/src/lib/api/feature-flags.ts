import { getSettingsFeatures } from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";

export type FeatureFlags = OpenAPIComponents["schemas"]["FeatureFlags"];
export type FeatureFlagKey = keyof FeatureFlags;

export async function getFeatureFlags(
  signal?: AbortSignal,
): Promise<FeatureFlags> {
  const response = await getSettingsFeatures({ signal });
  return response.data ?? {};
}
