import { Tooltip } from "@/components/ui/tooltip";
import { formatRelativeTime } from "@/lib/utils";

/** Exact local timestamp for a tooltip, or undefined when the value is absent/invalid. */
export function exactTimestamp(value: string | null | undefined) {
  if (!value) return undefined;
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) {
    return undefined;
  }
  return date.toLocaleString();
}

/** Relative timestamp ("3 days ago") with the exact time in a Tooltip. */
export function AgeCell({
  value,
  empty = "—",
}: {
  value: string | null | undefined;
  empty?: string;
}) {
  const exact = exactTimestamp(value);
  if (!exact) {
    return <span className="text-xs text-muted-foreground">{empty}</span>;
  }
  return (
    <Tooltip content={exact}>
      <span className="text-xs text-muted-foreground">
        {formatRelativeTime(value)}
      </span>
    </Tooltip>
  );
}
