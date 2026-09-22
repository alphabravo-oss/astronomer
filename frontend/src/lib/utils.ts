import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";
import { format, formatDistanceToNow, parseISO } from "date-fns";
import type {
  DateFormatPreference,
  TimeFormatPreference,
} from "@/lib/api/user-preferences";

let activeTimeFormat: TimeFormatPreference = "locale";
let activeDateFormat: DateFormatPreference = "locale";

export function setTimeFormatPreference(preference: TimeFormatPreference) {
  activeTimeFormat = preference;
}

export function setDateFormatPreference(preference: DateFormatPreference) {
  activeDateFormat = preference;
}

/**
 * Merge Tailwind CSS classes with proper precedence
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Format a date string to a human-readable format. An explicit `fmt`
 * (date-fns pattern) always wins; otherwise the `date_format` preference
 * picks the overall style ("iso" / "relative" bypass `time_format`
 * entirely, since neither has a 12h/24h axis), and `time_format` only
 * matters for the default "locale" style's hour rendering.
 */
export function formatDate(
  dateStr: string,
  fmt?: string,
): string {
  try {
    const date = parseISO(dateStr);
    if (fmt) return format(date, fmt);
    if (activeDateFormat === "iso") return date.toISOString();
    if (activeDateFormat === "relative") {
      return formatDistanceToNow(date, { addSuffix: true });
    }
    if (activeTimeFormat === "12h") {
      return format(date, "MMM d, yyyy h:mm a");
    }
    if (activeTimeFormat === "24h") {
      return format(date, "MMM d, yyyy HH:mm");
    }
    return date.toLocaleString();
  } catch {
    return dateStr;
  }
}

/**
 * Format a date string to a relative time (e.g., "2 hours ago")
 */
export function formatRelativeTime(
  dateStr: string | null | undefined,
): string {
  if (!dateStr) return "Never";
  try {
    const date = parseISO(dateStr);
    // Go and SQL zero timestamps are absence sentinels, not historical events.
    if (date.getUTCFullYear() <= 1 || date.getTime() === 0) return "Never";
    return formatDistanceToNow(date, { addSuffix: true });
  } catch {
    return dateStr;
  }
}

/**
 * Format bytes to human-readable format (e.g., "1.5 GiB")
 */
export function formatBytes(
  bytes: number | null | undefined,
  decimals: number = 1,
): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"];
  const i = Math.min(
    Math.floor(Math.log(bytes) / Math.log(k)),
    sizes.length - 1,
  );
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(decimals))} ${sizes[i]}`;
}

/**
 * Format CPU millicores to human-readable format.
 *
 * Inputs can be raw floats from prometheus (e.g. 118.99999999999999 from
 * a rate() query that lost a hair of precision); the millicore branch
 * rounds to an integer so the UI never renders a 15-digit tail. The
 * cores branch caps at one decimal and strips trailing zeros so we get
 * "2 cores" not "2.0 cores".
 */
export function formatCPU(millicores: number): string {
  if (millicores == null || isNaN(millicores)) return "—";
  if (millicores >= 1000) {
    return `${parseFloat((millicores / 1000).toFixed(1))} cores`;
  }
  return `${Math.round(millicores)}m`;
}

/**
 * Format a percentage value. Returns "—" for null/undefined/NaN inputs so the
 * caller can distinguish "no data" from a real 0%. Trailing zeros after the
 * decimal point are stripped ("50%" not "50.0%").
 */
export function formatPercentage(
  value: number | undefined | null,
  decimals: number = 1,
): string {
  if (value == null || isNaN(value)) return "—";
  return `${parseFloat(value.toFixed(decimals))}%`;
}

type StatusTone =
  "success" | "warning" | "error" | "info" | "pending" | "neutral" | "high";

const statusToneByKey: Record<string, StatusTone> = {
  active: "success",
  healthy: "success",
  running: "success",
  ready: "success",
  synced: "success",
  insync: "success",
  succeeded: "success",
  completed: "success",
  connected: "success",
  success: "success",
  allowed: "success",
  permitted: "success",
  enabled: "success",
  compliant: "success",
  applied: "success",
  pass: "success",
  private: "success",

  warning: "warning",
  warn: "warning",
  degraded: "warning",
  outofsync: "warning",
  drifted: "warning",
  drifting: "warning",
  stale: "warning",
  readonly: "warning",
  migrationrequired: "warning",
  decommissioning: "warning",
  medium: "warning",

  error: "error",
  critical: "error",
  failed: "error",
  fail: "error",
  unhealthy: "error",
  notready: "error",
  denied: "error",
  forbidden: "error",
  blocked: "error",
  missing: "error",
  noncompliant: "error",
  incident: "error",

  high: "high",

  pending: "info",
  connecting: "info",
  provisioning: "info",
  progressing: "info",
  installing: "info",
  info: "info",
  low: "info",

  disconnected: "neutral",
  unknown: "neutral",
  suspended: "neutral",
  disabled: "neutral",
  unmanaged: "neutral",
  skip: "neutral",
};

function statusTone(status: string): StatusTone {
  return (
    statusToneByKey[status.toLowerCase().replace(/[\s_-]/g, "")] ?? "neutral"
  );
}

export function statusColor(status: string): string {
  return `text-status-${statusTone(status)}`;
}

export function statusBgColor(status: string): string {
  const tone = statusTone(status);
  return `bg-status-${tone}/10 text-status-${tone}`;
}

export function statusDotColor(status: string): string {
  return `bg-status-${statusTone(status)}`;
}

/**
 * Get provider display name
 */
export function providerDisplayName(provider: string): string {
  const names: Record<string, string> = {
    aws: "AWS",
    gcp: "GCP",
    azure: "Azure",
    "on-prem": "On-Premise",
    digitalocean: "DigitalOcean",
    other: "Other",
  };
  return names[provider] || provider;
}

/**
 * Convert a cluster distribution slug to a display name.
 */
export function distributionDisplayName(distribution: string): string {
  const names: Record<string, string> = {
    k3s: "K3s",
    rke2: "RKE2",
    eks: "Amazon EKS",
    aks: "Azure AKS",
    gke: "Google GKE",
    openshift: "OpenShift",
    k8s: "Kubernetes",
  };
  return names[distribution] || distribution || "Unknown";
}

/**
 * Truncate text with ellipsis
 */
export function truncate(str: string, maxLength: number): string {
  if (str.length <= maxLength) return str;
  return str.slice(0, maxLength) + "...";
}

/**
 * Generate a deterministic color from a string (for avatars, tags, etc.)
 */
export function stringToColor(str: string): string {
  let hash = 0;
  for (let i = 0; i < str.length; i++) {
    hash = str.charCodeAt(i) + ((hash << 5) - hash);
  }
  const hue = Math.abs(hash % 360);
  return `hsl(${hue}, 60%, 50%)`;
}

/**
 * Copy text to clipboard
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/**
 * Determine gauge color based on percentage thresholds
 */
export function gaugeColor(percentage: number): string {
  if (percentage >= 90) return "bg-status-error";
  if (percentage >= 75) return "bg-status-warning";
  return "bg-status-success";
}

/**
 * Determine gauge text color based on percentage thresholds
 */
export function gaugeTextColor(percentage: number): string {
  if (percentage >= 90) return "text-status-error";
  if (percentage >= 75) return "text-status-warning";
  return "text-status-success";
}

/**
 * Format a Kubernetes version for display.
 *
 * The agent reports the version straight from the Kubernetes API, which already
 * carries its own leading "v" ("v1.30.4+k3s1"). Call sites that hardcoded a `v`
 * prefix rendered "vv1.30.4+k3s1". Only add the v when it isn't already there.
 */
export function formatK8sVersion(version: string | null | undefined): string {
  const v = (version ?? "").trim();
  if (!v) return "—";
  return /^v/i.test(v) ? v : `v${v}`;
}

/**
 * Capitalize the first character of a string (leaving the rest untouched).
 */
export function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/**
 * Convert a `#rrggbb` hex color into the `H S% L%` triplet format the
 * theme's CSS custom properties (`--primary`, `--status-*`, …) use — e.g.
 * `#3b82f6` -> `"217 91% 60%"`. Returns `null` for anything that isn't a
 * strict 6-digit hex color so callers can skip applying an operator-supplied
 * value that doesn't parse instead of writing garbage into the stylesheet.
 */
export function hexToHslTriplet(hex: string): string | null {
  const match = /^#?([0-9a-fA-F]{6})$/.exec(hex.trim());
  if (!match) return null;
  const value = match[1];
  const r = parseInt(value.slice(0, 2), 16) / 255;
  const g = parseInt(value.slice(2, 4), 16) / 255;
  const b = parseInt(value.slice(4, 6), 16) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const delta = max - min;
  const l = (max + min) / 2;
  let h = 0;
  let s = 0;
  if (delta !== 0) {
    s = delta / (1 - Math.abs(2 * l - 1));
    switch (max) {
      case r:
        h = 60 * (((g - b) / delta) % 6);
        break;
      case g:
        h = 60 * ((b - r) / delta + 2);
        break;
      default:
        h = 60 * ((r - g) / delta + 4);
    }
  }
  if (h < 0) h += 360;
  return `${Math.round(h)} ${Math.round(s * 100)}% ${Math.round(l * 100)}%`;
}

/**
 * Trigger a browser download of `content` as `filename`.
 *
 * `content` may be a ready `Blob` or any `BlobPart` (string, ArrayBuffer, …);
 * in the latter case it is wrapped into a `Blob` with the optional `mime` type.
 */
export function downloadBlob(
  content: Blob | BlobPart,
  filename: string,
  mime?: string,
): void {
  const blob =
    content instanceof Blob
      ? content
      : new Blob([content], mime ? { type: mime } : undefined);
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
