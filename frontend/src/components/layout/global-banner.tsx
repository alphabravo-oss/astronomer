import { useBanner } from "@/lib/hooks/public-settings";

const colorClass = {
  info: "bg-status-info/15 text-status-info border-status-info/30",
  warning: "bg-status-warning/15 text-status-warning border-status-warning/30",
  critical: "bg-status-error/15 text-status-error border-status-error/30",
} as const;

/**
 * Global classification/compliance banner (regulated customers use this as a
 * control, not decoration). Public, unauthenticated endpoint — must degrade
 * to rendering nothing on any failure rather than blocking the shell. Not
 * dismissible: an operator-configured banner stays visible for the session.
 */
export function GlobalBanner() {
  const { data } = useBanner();
  const text = data?.["banner.global_text"];
  if (!text) return null;
  const color = data?.["banner.global_color"] ?? "info";

  return (
    <div
      role="status"
      className={`flex items-center gap-2 border-b px-4 py-2 text-sm ${colorClass[color]}`}
    >
      {text}
    </div>
  );
}
