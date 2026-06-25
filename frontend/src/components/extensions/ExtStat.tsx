'use client';

// §Schema Tier-1 — stat renderer. A single proxied object -> one labelled value
// plus an optional delta. Text-only: value and delta go through closed-enum
// formatters into text nodes. Reuses the host MetricCard shell so an extension
// stat matches first-party metric cards.

import { MetricCard } from '@/components/ui/metric-card';
import { getByPath, formatValue, type ProxyRow } from './declarative';
import type { StatSpec } from '@/lib/api/extensions';

export interface ExtStatProps {
  row: ProxyRow;
  spec: StatSpec;
  emptyText?: string;
}

// Direction of the delta drives the MetricCard trend arrow. A positive number
// trends up, negative down, zero/non-numeric flat. Pure formatting; the value
// shown is always the formatted delta string.
function deltaTrend(raw: unknown): 'up' | 'down' | 'flat' {
  const n = typeof raw === 'number' ? raw : Number(raw);
  if (!Number.isFinite(n) || n === 0) return 'flat';
  return n > 0 ? 'up' : 'down';
}

export function ExtStat({ row, spec, emptyText }: ExtStatProps) {
  const rawValue = getByPath(row, spec.value.path);

  if (rawValue === null || rawValue === undefined) {
    return (
      <div className="rounded-lg border border-border bg-card p-5 text-sm text-muted-foreground">
        {emptyText || 'No data'}
      </div>
    );
  }

  const value = formatValue(rawValue, spec.value.format);
  const rawDelta = spec.delta ? getByPath(row, spec.delta.path) : undefined;
  const hasDelta = spec.delta && rawDelta !== null && rawDelta !== undefined;

  return (
    <MetricCard
      title={spec.label}
      value={value}
      trend={hasDelta ? deltaTrend(rawDelta) : undefined}
      trendValue={hasDelta ? formatValue(rawDelta, spec.delta!.format) : undefined}
    />
  );
}
