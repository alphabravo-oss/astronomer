import { type ReactNode } from "react";

/** Renders the same content in a light and a scoped `.dark` surface. */
export function Showcase({
  id,
  title,
  children,
}: {
  id: string;
  title: string;
  children: ReactNode;
}) {
  return (
    <section data-testid={`gallery-${id}`} aria-labelledby={`gallery-${id}-h`}>
      <h2
        id={`gallery-${id}-h`}
        className="mb-2 text-section-title font-semibold text-foreground"
      >
        {title}
      </h2>
      <div className="grid gap-4 xl:grid-cols-2">
        {(["light", "dark"] as const).map((theme) => (
          <div
            key={theme}
            data-gallery-theme={theme}
            className={`${theme === "dark" ? "dark " : ""}min-w-0 space-y-4 rounded-lg border border-border bg-background p-4 text-foreground`}
          >
            <div className="text-xs uppercase tracking-wider text-muted-foreground">
              {theme}
            </div>
            {children}
          </div>
        ))}
      </div>
    </section>
  );
}

export function Row({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap items-center gap-2">{children}</div>;
}
