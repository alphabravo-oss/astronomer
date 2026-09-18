import type { AnchorHTMLAttributes, ReactNode } from "react";

type Search = Record<string, string | number | boolean | null | undefined>;

function buildHref(
  to: string,
  params: Record<string, string> | undefined,
  search: Search | undefined,
  hash: string | undefined,
): string {
  const path = to.replace(/\$([A-Za-z][A-Za-z0-9_]*)/g, (_, key: string) =>
    encodeURIComponent(params?.[key] ?? `$${key}`),
  );
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(search ?? {})) {
    if (value != null) query.set(key, String(value));
  }
  return `${path}${query.size ? `?${query}` : ""}${hash ? `#${hash}` : ""}`;
}

/** A DOM-only Link for unit tests that deliberately do not mount a router. */
export function RouterLinkStub({
  to,
  params,
  search,
  hash,
  children,
  ...props
}: Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "href"> & {
  to: string;
  params?: Record<string, string>;
  search?: Search;
  hash?: string;
  children: ReactNode;
}) {
  return (
    <a href={buildHref(to, params, search, hash)} {...props}>
      {children}
    </a>
  );
}
