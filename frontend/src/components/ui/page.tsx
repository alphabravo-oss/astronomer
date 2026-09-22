import type { ReactNode } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { cn } from "@/lib/utils";

export function PageShell({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return <div className={cn("space-y-6", className)}>{children}</div>;
}

export function PageHeader({
  title,
  description,
  eyebrow,
  actions,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  eyebrow?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between",
        className,
      )}
    >
      <div className="min-w-0">
        {eyebrow ? (
          <div className="mb-1 text-xs font-medium uppercase tracking-wider text-muted-foreground">
            {eyebrow}
          </div>
        ) : null}
        <h1 className="truncate text-2xl font-semibold tracking-tight text-foreground">
          {title}
        </h1>
        {description ? (
          <p className="mt-1 max-w-3xl text-sm text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {actions}
        </div>
      ) : null}
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
  className?: string;
}) {
  return (
    <div className={cn("space-y-3", className)}>
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
                "truncate text-2xl font-semibold tracking-tight text-foreground",
                mono && "font-mono",
              )}
            >
              {title}
            </h1>
            {status}
          </div>
          {meta.length > 0 ? (
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
        </div>
        {actions ? (
          <div className="flex shrink-0 flex-wrap items-center gap-2">
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
              <h2 className="text-sm font-semibold text-foreground">{title}</h2>
            ) : null}
            {description ? (
              <p className="mt-1 text-sm text-muted-foreground">
                {description}
              </p>
            ) : null}
          </div>
          {actions ? (
            <div className="flex shrink-0 flex-wrap items-center gap-2">
              {actions}
            </div>
          ) : null}
        </div>
      ) : null}
      {children}
    </section>
  );
}
