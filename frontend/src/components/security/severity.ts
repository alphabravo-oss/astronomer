/**
 * Phase B5 — CIS scan colour scheme.
 *
 * Per the spec the severity palette is fixed:
 *   critical = red, high = orange, medium = amber, low = blue.
 * Status (pass/fail/warn/skip) reuses the existing platform tokens.
 *
 * Severities arrive in a few different cases from cis-operator (`Critical`,
 * `HIGH`, etc.); we lower-case before lookup so callers don't have to.
 */

import type { CISFindingSeverity, CISFindingStatus } from '@/types';

/** Tailwind class fragments for severity badges. */
export const severityBadge: Record<string, string> = {
  critical: 'bg-red-500/10 text-red-500 border border-red-500/20',
  high: 'bg-orange-500/10 text-orange-500 border border-orange-500/20',
  medium: 'bg-amber-500/10 text-amber-500 border border-amber-500/20',
  low: 'bg-blue-500/10 text-blue-500 border border-blue-500/20',
  info: 'bg-muted text-muted-foreground border border-border',
};

/** Tailwind class fragments for finding status badges. */
export const findingStatusBadge: Record<string, string> = {
  pass: 'bg-status-success/10 text-status-success',
  fail: 'bg-status-error/10 text-status-error',
  warn: 'bg-status-warning/10 text-status-warning',
  skip: 'bg-muted text-muted-foreground',
  info: 'bg-status-info/10 text-status-info',
};

export function severityClass(s: CISFindingSeverity | undefined | null): string {
  const key = String(s ?? '').toLowerCase();
  return severityBadge[key] ?? severityBadge.info;
}

export function findingStatusClass(s: CISFindingStatus | undefined | null): string {
  const key = String(s ?? '').toLowerCase();
  return findingStatusBadge[key] ?? findingStatusBadge.info;
}

/**
 * Numeric weight used for sort & filter ordering. Critical first, info
 * last; an unknown severity goes after `low` so it doesn't crowd the top.
 */
export function severityRank(s: CISFindingSeverity | undefined | null): number {
  const map: Record<string, number> = {
    critical: 0,
    high: 1,
    medium: 2,
    low: 3,
    info: 4,
  };
  const key = String(s ?? '').toLowerCase();
  return map[key] ?? 5;
}

/** All known severities in display order. */
export const SEVERITY_ORDER: CISFindingSeverity[] = ['critical', 'high', 'medium', 'low'];

/** All filterable finding statuses in display order. */
export const STATUS_ORDER: CISFindingStatus[] = ['fail', 'warn', 'pass', 'skip'];
