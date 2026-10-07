/** Composite quota cell: `CPU 8 / Mem 16Gi` on one line, right-aligned. */
export function QuotaCell({ cpu, mem }: { cpu?: string; mem?: string }) {
  if (!cpu && !mem) {
    return <span className="text-xs text-muted-foreground">No quota</span>;
  }
  return (
    <span className="whitespace-nowrap font-mono text-xs tabular-nums text-muted-foreground">
      {[cpu ? `CPU ${cpu}` : null, mem ? `Mem ${mem}` : null]
        .filter(Boolean)
        .join(" / ")}
    </span>
  );
}
