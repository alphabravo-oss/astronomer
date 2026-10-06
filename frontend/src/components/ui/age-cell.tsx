import { Tooltip } from "@/components/ui/tooltip";

/** Exact local timestamp for a tooltip, or undefined when the value is absent/invalid. */
export function exactTimestamp(value: string | null | undefined) {
  if (!value) return undefined;
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) {
    return undefined;
  }
  return date.toLocaleString();
}

/** Compact relative age ("20h ago", "3d ago"); the exact time lives in the Tooltip. */
export function compactAge(value: string, now: number = Date.now()): string {
  const seconds = Math.max(
    0,
    Math.floor((now - new Date(value).getTime()) / 1000),
  );
  if (seconds < 60) return "just now";
  const units: [number, string][] = [
    [365 * 86400, "y"],
    [30 * 86400, "mo"],
    [86400, "d"],
    [3600, "h"],
    [60, "m"],
  ];
  for (const [size, label] of units) {
    if (seconds >= size) return `${Math.floor(seconds / size)}${label} ago`;
  }
  return "just now";
}

/** Compact relative timestamp with the exact time in a Tooltip. */
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
        {compactAge(value as string)}
      </span>
    </Tooltip>
  );
}
