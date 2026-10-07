import { createContext, useContext, type ReactNode } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { Skeleton, SkeletonText } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

export function PageShell({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("space-y-(--gap-section)", className)}>{children}</div>
  );
}

/**
 * Layout routes (e.g. Delivery) can publish a scope line here; every
 * `PageHeader` below them that does not set its own `eyebrow` shows it, so the
 * scope notice lives in the header's eyebrow slot instead of floating above it.
 */
const PageEyebrowContext = createContext<ReactNode>(null);
export const PageEyebrowProvider = PageEyebrowContext.Provider;

/**
 * Fixed slots, top to bottom: `eyebrow` (scope line), `title` (+ inline
 * `status`), `description` (one sentence), `tabs` (route navigation: a `<nav>`
 * of links, never role=tablist). `actions` sit on the right of the title block.
 * No page-level icon next to the title: icons belong to nav, not headings.
 */
export function PageHeader({
  title,
  description,
  eyebrow,
  status,
  actions,
  tabs,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  eyebrow?: ReactNode;
  status?: ReactNode;
  actions?: ReactNode;
  tabs?: ReactNode;
  className?: string;
}) {
  const scopeEyebrow = useContext(PageEyebrowContext);
  const eyebrowContent = eyebrow ?? scopeEyebrow;
  return (
    <div className={cn("space-y-3", className)}>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          {eyebrowContent ? (
            <div className="mb-1 text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {eyebrowContent}
            </div>
          ) : null}
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <h1 className="truncate text-page-title font-semibold tracking-tight text-foreground">
              {title}
            </h1>
            {status}
          </div>
          {description ? (
            <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
              {description}
            </p>
          ) : null}
        </div>
        {actions ? (
          <div className="flex min-w-0 max-w-full flex-wrap items-center gap-2">
            {actions}
          </div>
        ) : null}
      </div>
      {tabs ? <div className="border-b border-border">{tabs}</div> : null}
    </div>
  );
}

/**
 * Detail-page masthead: back link, eyebrow, title (+ inline status), actions,
 * a metadata `<dl>` row, and an optional description. Introduced so the five
 * hand-rolled resource/cluster detail headers (icon-button back link + h1 +
 * inline meta spans) converge on one primitive instead of re-inventing the
 * layout per page (see docs/design-system.md).
 */
export function ResourceMasthead({
  backTo,
  onBack,
  backLabel = "Back",
  eyebrow,
  title,
  mono = false,
  status,
  meta = [],
  actions,
  description,
  details,
  loading = false,
  className,
}: {
  backTo?: string;
  /** Use instead of `backTo` when the back action isn't a route navigation (e.g. `window.history.back()`). */
  onBack?: () => void;
  backLabel?: string;
  eyebrow?: ReactNode;
  title: ReactNode;
  mono?: boolean;
  status?: ReactNode;
  meta?: Array<{ label: string; value: ReactNode }>;
  actions?: ReactNode;
  description?: ReactNode;
  /** Rich rows under the meta line (label/annotation chips, conditions strip). */
  details?: ReactNode;
  /** Render skeleton placeholders for the title and meta while the object loads. */
  loading?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn("space-y-3", className)}
      aria-busy={loading || undefined}
    >
      <div className="flex flex-wrap items-start gap-4">
        {backTo ? (
          <RouterLink
            to={backTo}
            className="mt-1 shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            aria-label={backLabel}
          >
            <ArrowLeft className="h-5 w-5" />
          </RouterLink>
        ) : onBack ? (
          <button
            type="button"
            onClick={onBack}
            className="mt-1 shrink-0 rounded-md p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            aria-label={backLabel}
          >
            <ArrowLeft className="h-5 w-5" />
          </button>
        ) : null}
        <div className="min-w-0 flex-1">
          {eyebrow ? (
            <div className="mb-1 text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {eyebrow}
            </div>
          ) : null}
          <div className="flex flex-wrap items-center gap-2">
            <h1
              className={cn(
                "truncate text-page-title font-semibold tracking-tight text-foreground",
                mono && "font-mono",
              )}
            >
              {title}
            </h1>
            {loading ? <Skeleton className="h-5 w-16 rounded-full" /> : status}
          </div>
          {loading ? (
            <SkeletonText lines={1} className="mt-2 max-w-md" />
          ) : meta.length > 0 ? (
            <dl className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">
              {meta.map((item, index) => (
                <div key={index} className="flex items-center gap-1">
                  <dt className="sr-only">{item.label}</dt>
                  <dd>
                    {item.label}: {item.value}
                  </dd>
                </div>
              ))}
            </dl>
          ) : null}
          {description ? (
            <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
              {description}
            </p>
          ) : null}
          {details && !loading ? (
            <div className="mt-3 space-y-2">{details}</div>
          ) : null}
        </div>
        {actions ? (
          <div className="flex min-w-0 max-w-full flex-wrap items-center gap-2">
            {actions}
          </div>
        ) : null}
      </div>
    </div>
  );
}

export function PageSection({
  children,
  title,
  description,
  actions,
  className,
}: {
  children: ReactNode;
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <section className={cn("space-y-3", className)}>
      {title || description || actions ? (
        <div className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
          <div className="min-w-0">
            {title ? (
              <h2 className="text-section-title font-semibold text-foreground">
                {title}
              </h2>
            ) : null}
            {description ? (
              <p className="mt-1 text-sm text-muted-foreground">
                {description}
              </p>
            ) : null}
          </div>
          {actions ? (
            <div className="flex min-w-0 max-w-full flex-wrap items-center gap-2">
              {actions}
            </div>
          ) : null}
        </div>
      ) : null}
      {children}
    </section>
  );
}
