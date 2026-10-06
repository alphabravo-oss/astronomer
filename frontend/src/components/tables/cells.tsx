import { useState, type ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { cn, formatDate, formatRelativeTime } from "@/lib/utils";

/**
 * Small shared cell renderers for DataTable columns that the generic column
 * kinds cannot express on their own (plan 031 P6b.4).
 */

/** Split `registry/repo:tag@sha256:...` into display parts. */
export function splitImageRef(ref: string): {
  repo: string;
  tag: string;
  digest: string;
} {
  const at = ref.indexOf("@");
  const digest = at >= 0 ? ref.slice(at + 1) : "";
  const nameTag = at >= 0 ? ref.slice(0, at) : ref;
  const lastSlash = nameTag.lastIndexOf("/");
  const colon = nameTag.indexOf(":", lastSlash + 1);
  if (colon < 0) return { repo: nameTag, tag: "", digest };
  return {
    repo: nameTag.slice(0, colon),
    tag: nameTag.slice(colon + 1),
    digest,
  };
}

/**
 * Image reference with the tag always visible: the registry/repository part
 * shrinks with an ellipsis, the `:tag` never does. The full reference
 * (including digest) is in the Tooltip. Extra images collapse to `+N`.
 */
export function ImageRefCell({
  image,
  extra = 0,
}: {
  image: string | null | undefined;
  extra?: number;
}) {
  if (!image) return <span className="text-muted-foreground">—</span>;
  const { repo, tag, digest } = splitImageRef(image);
  return (
    <Tooltip content={image} side="top">
      <span
        data-cell-clip=""
        className="flex min-w-0 items-baseline font-mono text-xs"
      >
        <span className="min-w-0 truncate">{repo}</span>
        {tag ? (
          <span className="shrink-0 text-muted-foreground">:{tag}</span>
        ) : digest ? (
          <span className="shrink-0 text-muted-foreground">
            @{digest.slice(0, 15)}
          </span>
        ) : null}
        {extra > 0 ? (
          <span className="ml-1 shrink-0 rounded-sm bg-muted px-1 text-2xs text-muted-foreground">
            +{extra}
          </span>
        ) : null}
      </span>
    </Tooltip>
  );
}

/** Chips capped at `max` with a `+N` overflow chip that lists the rest in a Tooltip. */
export function ChipsCell({
  items,
  max = 2,
  empty = "—",
  chipMaxClass = "max-w-36",
}: {
  items: readonly string[];
  max?: number;
  empty?: ReactNode;
  chipMaxClass?: string;
}) {
  if (items.length === 0)
    return <span className="text-muted-foreground">{empty}</span>;
  const shown = items.slice(0, max);
  const rest = items.slice(max);
  const chip =
    "shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground";
  return (
    <span
      data-cell-clip=""
      className="flex min-w-0 items-center gap-1 overflow-hidden"
    >
      {shown.map((item) => (
        <span key={item} className={cn(chip, chipMaxClass, "truncate")}>
          {item}
        </span>
      ))}
      {rest.length > 0 ? (
        <Tooltip content={rest.join(", ")}>
          <span className={chip}>+{rest.length}</span>
        </Tooltip>
      ) : null}
    </span>
  );
}

/**
 * Compact relative age: `now`, `14m`, `3h`, `3d`, `5mo`, `2y`. Short enough
 * for an 80px column; the exact timestamp lives in the Tooltip.
 */
export function shortAge(value: string, now: number = Date.now()): string {
  const diff = Math.max(0, now - Date.parse(value));
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 1) return "now";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d`;
  if (days < 365) return `${Math.floor(days / 30)}mo`;
  return `${Math.floor(days / 365)}y`;
}

/**
 * Compact relative time with the exact timestamp in a Tooltip. Pass
 * `suffix` for "last seen" style columns (`14m ago`).
 */
export function TimestampCell({
  value,
  fallback = "Never",
  suffix = false,
  className,
}: {
  value: string | null | undefined;
  fallback?: string;
  suffix?: boolean;
  className?: string;
}) {
  const absent = (
    <span className={cn("text-muted-foreground", className)}>{fallback}</span>
  );
  // Go and SQL zero timestamps are absence sentinels, not events.
  if (!value || formatRelativeTime(value) === "Never") return absent;
  const parsed = Date.parse(value);
  if (Number.isNaN(parsed)) return <span className={className}>{value}</span>;
  const age = shortAge(value);
  return (
    <Tooltip content={formatDate(value)}>
      <span className={cn("whitespace-nowrap", className)}>
        {suffix && age !== "now" ? `${age} ago` : age}
      </span>
    </Tooltip>
  );
}

/** Utilization bar with a percent label; `detail` (used / capacity) is the Tooltip. */
export function UsageGauge({
  pct,
  label,
  detail,
}: {
  pct: number;
  label: string;
  detail?: ReactNode;
}) {
  const tone =
    pct >= 90
      ? "bg-status-error"
      : pct >= 75
        ? "bg-status-warning"
        : "bg-status-success";
  return (
    <Tooltip content={detail}>
      <span className="flex items-center justify-end gap-1.5">
        <span className="gauge-bar block w-8 shrink-0">
          <span
            className={cn("gauge-bar-fill block", tone)}
            style={{ width: `${Math.min(Math.max(pct, 0), 100)}%` }}
          />
        </span>
        <span className="min-w-8 text-xs tabular-nums text-muted-foreground">
          {label}
        </span>
      </span>
    </Tooltip>
  );
}

/**
 * Two-line entity cell (name + subtitle). Each line truncates on its own; the
 * Tooltip shows both lines in full, but only while something is clipped.
 */
export function EntityCell({
  primary,
  secondary,
  mono = false,
  primaryClassName,
}: {
  primary: ReactNode;
  secondary?: ReactNode;
  mono?: boolean;
  primaryClassName?: string;
}) {
  const [full, setFull] = useState<string | undefined>();
  const measure = (root: HTMLElement) => {
    const clipped = Array.from(root.children).some(
      (el) => el.scrollWidth > el.clientWidth + 1,
    );
    setFull(
      clipped ? root.innerText || root.textContent || undefined : undefined,
    );
  };
  return (
    <Tooltip content={full}>
      <div
        data-cell-clip=""
        className="min-w-0"
        onPointerEnter={(event) => measure(event.currentTarget)}
      >
        <div
          className={cn(
            "truncate font-medium text-foreground",
            mono && "font-mono text-xs",
            primaryClassName,
          )}
        >
          {primary}
        </div>
        {secondary != null && secondary !== "" ? (
          <div className="truncate text-xs text-muted-foreground">
            {secondary}
          </div>
        ) : null}
      </div>
    </Tooltip>
  );
}

/** Status pill with an optional reason line underneath (composite cell). */
export function StatusReasonCell({
  status,
  reason,
}: {
  status: ReactNode;
  reason?: string | null;
}) {
  return (
    <div className="min-w-0 space-y-0.5">
      {status}
      {reason ? (
        <Tooltip content={reason}>
          <p className="truncate text-xs text-status-warning">{reason}</p>
        </Tooltip>
      ) : null}
    </div>
  );
}
