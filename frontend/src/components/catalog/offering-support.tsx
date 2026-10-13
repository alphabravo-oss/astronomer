import { Tooltip } from "@/components/ui/tooltip";
import {
  offeringSupport,
  type OfferingSupport as Support,
} from "@/lib/catalogs/assurance";

export function OfferingSupport({
  support = offeringSupport(),
  detail = false,
}: {
  support?: Support;
  detail?: boolean;
}) {
  return (
    <div className="space-y-1 text-xs">
      <Tooltip content={support.description}>
        <span
          className="inline-flex rounded-sm border border-border bg-muted/30 px-2 py-0.5 font-medium"
          aria-label={`Support: ${support.level}`}
        >
          {support.level}
          {support.level === "Assured" ? " profile" : ""}
        </span>
      </Tooltip>
      {detail && <p className="text-muted-foreground">{support.description}</p>}
      {detail && support.supportUntil && (
        <p className="text-muted-foreground">
          Support until {support.supportUntil}
        </p>
      )}
      {detail && support.evidenceURL && (
        <a
          className="text-primary underline"
          href={support.evidenceURL}
          target="_blank"
          rel="noreferrer"
        >
          Qualification evidence
        </a>
      )}
    </div>
  );
}
