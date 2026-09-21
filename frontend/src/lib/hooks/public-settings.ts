import { useQuery } from "@tanstack/react-query";
import { getPublicBanner, getPublicBranding } from "@/lib/api/public-settings";
import { queryKeys } from "@/lib/query-keys";

/**
 * Branding and banner settings are public (no auth) so the login screen and
 * the global banner can render before a session exists. Both hooks must
 * degrade quietly on failure — a branding/banner outage is cosmetic, never a
 * reason to block the dashboard shell or the login form. Callers treat
 * `data` as optional and fall back to today's defaults.
 */
export function useBranding() {
  return useQuery({
    queryKey: queryKeys.publicSettings.branding,
    queryFn: () => getPublicBranding(),
    staleTime: 5 * 60_000,
    retry: 1,
    throwOnError: false,
  });
}

export function useBanner() {
  return useQuery({
    queryKey: queryKeys.publicSettings.banner,
    queryFn: () => getPublicBanner(),
    staleTime: 5 * 60_000,
    retry: 1,
    throwOnError: false,
  });
}

/** The operator's product name, falling back to "Astronomer". */
export function useProductName(): string {
  const { data: branding } = useBranding();
  return branding?.["branding.product_name"] || "Astronomer";
}

/** Branding + banner fields the login screen renders, pre-defaulted. */
export function useLoginBranding() {
  const { data: branding } = useBranding();
  const { data: banner } = useBanner();
  return {
    productName: branding?.["branding.product_name"] || "Astronomer",
    logoUrl: branding?.["branding.logo_url"],
    loginBannerText: banner?.["banner.login_text"],
  };
}
