import { useId, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { BARE_BUTTON } from "@/lib/bare-button";
import { Tooltip } from "@/components/ui/tooltip";

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

export function fmtRelative(iso?: string): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const delta = Date.now() - t;
  const mins = Math.floor(delta / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

export function LastSeen({ iso }: { iso?: string }) {
  const valid = iso && !Number.isNaN(Date.parse(iso));
  return (
    <Tooltip content={valid ? new Date(iso).toLocaleString() : undefined}>
      <span className="text-muted-foreground">{fmtRelative(iso)}</span>
    </Tooltip>
  );
}

export const LAST_SEEN_COLUMN = {
  key: "lastSeen",
  header: "Last seen",
  kind: "age",
  minSize: 128,
} as const;

// parseQuantity converts a Kubernetes-style quantity string to a number
// where possible (so we can compute used/hard ratios). Returns NaN for
// anything we don't recognise — the UI degrades to a numerator/
// denominator string when that happens.
export function parseQuantity(v: string | number | undefined | null): number {
  if (v == null) return NaN;
  if (typeof v === "number") return v;
  const m = /^(\d+(?:\.\d+)?)([a-zA-Z]*)$/.exec(v.trim());
  if (!m) return Number.NaN;
  const num = parseFloat(m[1]);
  const suffix = m[2];
  const mult: Record<string, number> = {
    "": 1,
    Ki: 1024,
    Mi: 1024 ** 2,
    Gi: 1024 ** 3,
    Ti: 1024 ** 4,
    K: 1e3,
    M: 1e6,
    G: 1e9,
    T: 1e12,
    m: 1e-3,
  };
  return num * (mult[suffix] ?? 1);
}

// ---------------------------------------------------------------------
// Section primitive
// ---------------------------------------------------------------------

export function Section({
  title,
  icon,
  count,
  defaultOpen = true,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  count: number;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const contentId = useId();
  return (
    <div className="rounded-lg border bg-card mb-4">
      <ActionButton
        {...BARE_BUTTON}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-controls={contentId}
        className="flex w-full items-center justify-between p-4 text-left font-normal whitespace-normal shrink"
      >
        <div className="flex items-center gap-2">
          {icon}
          <h2 className="text-base font-semibold">{title}</h2>
          <span className="rounded-full bg-muted px-2 py-0.5 text-xs">
            {count}
          </span>
        </div>
        {open ? (
          <ChevronDown className="h-4 w-4" />
        ) : (
          <ChevronRight className="h-4 w-4" />
        )}
      </ActionButton>
      {open && (
        <div id={contentId} className="border-t p-4">
          {children}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Per-kind tables
// ---------------------------------------------------------------------
