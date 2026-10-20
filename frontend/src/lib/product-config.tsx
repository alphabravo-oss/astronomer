import { forwardRef, useMemo, type ReactNode } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import { Orbit } from "lucide-react";
import { ProductProvider, type ProductConfig, type ProductLinkProps } from "@/ui";
import { APP_VERSION } from "@/lib/env";
import { useBranding } from "@/lib/hooks/public-settings";
import { cn } from "@/lib/utils";

/** Router-aware link handed to the shared kit. */
const AstronomerLink = forwardRef<HTMLAnchorElement, ProductLinkProps>(
  ({ to, current, children, className, ...rest }, ref) => (
    <RouterLink
      ref={ref}
      to={to}
      // The kit decides what is current; TanStack's fuzzy active matching
      // would otherwise mark "/dashboard/clusters" current on every cluster
      // page.
      activeOptions={{ exact: true }}
      activeProps={{}}
      aria-current={current ? "page" : undefined}
      className={className}
      {...rest}
    >
      {children}
    </RouterLink>
  ),
);
AstronomerLink.displayName = "AstronomerLink";

/**
 * Astronomer's mark: the Orbit glyph on the brand gradient. An operator logo
 * (branding.logo_url) replaces it; the brand gradient collapses to the
 * operator's accent via `applyBrandAccent`.
 */
export function AstronomerMark({
  logoUrl,
  size = "sm",
  className,
}: {
  logoUrl?: string;
  size?: "sm" | "lg";
  className?: string;
}) {
  const dimension = size === "lg" ? "h-10 w-10 rounded-xl" : "h-7 w-7 rounded-lg";
  if (logoUrl) {
    return (
      <img
        src={logoUrl}
        alt=""
        className={cn(dimension, "object-contain", className)}
      />
    );
  }
  return (
    <span
      aria-hidden="true"
      className={cn(
        dimension,
        "flex items-center justify-center bg-linear-to-br from-brand-from to-brand-to text-white",
        className,
      )}
    >
      <Orbit className={size === "lg" ? "h-5 w-5" : "h-4 w-4"} />
    </span>
  );
}

export function AstronomerProductProvider({ children }: { children: ReactNode }) {
  const { data: branding } = useBranding();
  const name = branding?.["branding.product_name"] || "Astronomer";
  const logoUrl = branding?.["branding.logo_url"] || undefined;
  const config = useMemo<ProductConfig>(
    () => ({
      name,
      vendor: "AlphaBravo",
      version: APP_VERSION,
      mark: <AstronomerMark logoUrl={logoUrl} />,
      markLarge: <AstronomerMark logoUrl={logoUrl} size="lg" />,
      loginHero: "orbit",
      docsUrl: "/astronomer-docs/",
      Link: AstronomerLink,
    }),
    [logoUrl, name],
  );
  return <ProductProvider config={config}>{children}</ProductProvider>;
}
