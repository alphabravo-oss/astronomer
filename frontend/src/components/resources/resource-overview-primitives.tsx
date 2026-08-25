import type { ReactNode } from "react";

export function Section({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card">
      <h2 className="border-b border-border px-4 py-2.5 text-sm font-semibold text-foreground">
        {title}
      </h2>
      <div className="px-4 py-3">{children}</div>
    </div>
  );
}
export function KeyValueTable({
  entries,
  mask,
}: {
  entries: Array<[string, string]>;
  mask?: boolean;
}) {
  if (entries.length === 0) {
    return <p className="text-xs text-muted-foreground">None</p>;
  }
  return (
    <dl className="divide-y divide-border/60 text-xs">
      {entries.map(([key, value]) => (
        <div
          key={key}
          className="grid grid-cols-[minmax(0,12rem)_1fr] gap-4 py-1.5 first:pt-0 last:pb-0"
        >
          <dt className="break-all font-mono text-muted-foreground">{key}</dt>
          <dd className="break-all font-mono text-foreground">
            {mask ? "••••••••" : value}
          </dd>
        </div>
      ))}
    </dl>
  );
}
