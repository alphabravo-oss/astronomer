import type { CatalogSourcePresentation } from "@/lib/catalogs/source";

export function CatalogSourceBadge({
  source,
  compact = false,
}: {
  source: CatalogSourcePresentation;
  compact?: boolean;
}) {
  return (
    <span
      className="inline-flex max-w-full items-center gap-1.5 rounded-full border font-medium"
      style={{
        color: source.foreground,
        backgroundColor: source.background,
        borderColor: source.border,
        padding: compact ? "1px 7px" : "2px 8px",
        fontSize: compact ? "10px" : "11px",
      }}
      title={`${source.familyLabel} source · ${source.repositoryName}`}
    >
      <span
        aria-hidden="true"
        className="h-1.5 w-1.5 flex-none rounded-full"
        style={{ backgroundColor: source.foreground }}
      />
      <span className="truncate">{source.label}</span>
    </span>
  );
}
