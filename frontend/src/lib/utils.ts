import { type ClassValue, clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';
import { format, formatDistanceToNow, parseISO } from 'date-fns';

/**
 * Merge Tailwind CSS classes with proper precedence
 */
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/**
 * Format a date string to a human-readable format
 */
export function formatDate(dateStr: string, fmt: string = 'MMM d, yyyy HH:mm'): string {
  try {
    return format(parseISO(dateStr), fmt);
  } catch {
    return dateStr;
  }
}

/**
 * Format a date string to a relative time (e.g., "2 hours ago")
 */
export function formatRelativeTime(dateStr: string): string {
  try {
    return formatDistanceToNow(parseISO(dateStr), { addSuffix: true });
  } catch {
    return dateStr;
  }
}

/**
 * Format bytes to human-readable format (e.g., "1.5 GiB")
 */
export function formatBytes(bytes: number, decimals: number = 1): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
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
  if (millicores == null || isNaN(millicores)) return '—';
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
export function formatPercentage(value: number | undefined | null, decimals: number = 1): string {
  if (value == null || isNaN(value)) return '—';
  return `${parseFloat(value.toFixed(decimals))}%`;
}

type StatusTone = 'success' | 'warning' | 'error' | 'info' | 'pending' | 'neutral' | 'high';

const statusToneByKey: Record<string, StatusTone> = {
  active: 'success',
  healthy: 'success',
  running: 'success',
  ready: 'success',
  synced: 'success',
  insync: 'success',
  succeeded: 'success',
  completed: 'success',
  connected: 'success',
  success: 'success',
  allowed: 'success',
  permitted: 'success',
  enabled: 'success',
  compliant: 'success',
  applied: 'success',
  pass: 'success',
  private: 'success',

  warning: 'warning',
  warn: 'warning',
  degraded: 'warning',
  outofsync: 'warning',
  drifted: 'warning',
  stale: 'warning',
  readonly: 'warning',
  migrationrequired: 'warning',
  decommissioning: 'warning',
  medium: 'warning',

  error: 'error',
  critical: 'error',
  failed: 'error',
  fail: 'error',
  unhealthy: 'error',
  notready: 'error',
  denied: 'error',
  forbidden: 'error',
  blocked: 'error',
  missing: 'error',
  noncompliant: 'error',
  incident: 'error',

  high: 'high',

  pending: 'info',
  connecting: 'info',
  provisioning: 'info',
  progressing: 'info',
  installing: 'info',
  info: 'info',
  low: 'info',

  disconnected: 'neutral',
  unknown: 'neutral',
  suspended: 'neutral',
  disabled: 'neutral',
  unmanaged: 'neutral',
  skip: 'neutral',
};

function statusTone(status: string): StatusTone {
  return statusToneByKey[status.toLowerCase().replace(/[\s_-]/g, '')] ?? 'neutral';
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
    aws: 'AWS',
    gcp: 'GCP',
    azure: 'Azure',
    'on-prem': 'On-Premise',
    digitalocean: 'DigitalOcean',
    other: 'Other',
  };
  return names[provider] || provider;
}

/**
 * Convert a cluster distribution slug to a display name.
 */
export function distributionDisplayName(distribution: string): string {
  const names: Record<string, string> = {
    k3s: 'K3s',
    rke2: 'RKE2',
    eks: 'Amazon EKS',
    aks: 'Azure AKS',
    gke: 'Google GKE',
    openshift: 'OpenShift',
    k8s: 'Kubernetes',
  };
  return names[distribution] || distribution || 'Unknown';
}

/**
 * Truncate text with ellipsis
 */
export function truncate(str: string, maxLength: number): string {
  if (str.length <= maxLength) return str;
  return str.slice(0, maxLength) + '...';
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
  if (percentage >= 90) return 'bg-status-error';
  if (percentage >= 75) return 'bg-status-warning';
  return 'bg-status-success';
}

/**
 * Determine gauge text color based on percentage thresholds
 */
export function gaugeTextColor(percentage: number): string {
  if (percentage >= 90) return 'text-status-error';
  if (percentage >= 75) return 'text-status-warning';
  return 'text-status-success';
}

/**
 * Format a Kubernetes version for display.
 *
 * The agent reports the version straight from the Kubernetes API, which already
 * carries its own leading "v" ("v1.30.4+k3s1"). Call sites that hardcoded a `v`
 * prefix rendered "vv1.30.4+k3s1". Only add the v when it isn't already there.
 */
export function formatK8sVersion(version: string | null | undefined): string {
  const v = (version ?? '').trim();
  if (!v) return '—';
  return /^v/i.test(v) ? v : `v${v}`;
}

/**
 * Capitalize the first character of a string (leaving the rest untouched).
 */
export function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/**
 * Trigger a browser download of `content` as `filename`.
 *
 * `content` may be a ready `Blob` or any `BlobPart` (string, ArrayBuffer, …);
 * in the latter case it is wrapped into a `Blob` with the optional `mime` type.
 */
export function downloadBlob(content: Blob | BlobPart, filename: string, mime?: string): void {
  const blob = content instanceof Blob ? content : new Blob([content], mime ? { type: mime } : undefined);
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
