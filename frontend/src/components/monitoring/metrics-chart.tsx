'use client';

import { useMemo } from 'react';
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from 'recharts';
import { format, parseISO } from 'date-fns';
import { formatBytes, formatCPU, cn } from '@/lib/utils';
import type { MetricsSeries } from '@/types';

interface MetricsChartProps {
  title: string;
  series: MetricsSeries[];
  unit?: string;
  height?: number;
  className?: string;
}

const CHART_COLORS = [
  { stroke: '#3b82f6', fill: '#3b82f6' },  // blue
  { stroke: '#6366f1', fill: '#6366f1' },  // indigo
  { stroke: '#10b981', fill: '#10b981' },  // green
  { stroke: '#f59e0b', fill: '#f59e0b' },  // amber
  { stroke: '#ef4444', fill: '#ef4444' },  // red
];

export function MetricsChart({
  title,
  series,
  unit = '',
  height = 280,
  className,
}: MetricsChartProps) {
  // Merge all series data into a single dataset keyed by timestamp
  const chartData = useMemo(() => {
    if (!series.length) return [];

    const timeMap = new Map<string, Record<string, number>>();

    series.forEach((s, idx) => {
      if (!s?.data) return;
      s.data.forEach((point) => {
        const existing = timeMap.get(point.timestamp) || {};
        existing[`series_${idx}`] = point.value;
        timeMap.set(point.timestamp, existing);
      });
    });

    return Array.from(timeMap.entries())
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([timestamp, values]) => ({
        timestamp,
        ...values,
      }));
  }, [series]);

  const formatValue = (value: number): string => {
    if (unit === 'bytes' || unit === 'bytes/s') return formatBytes(value);
    if (unit === 'millicores') return formatCPU(value);
    if (unit === '%') return `${value.toFixed(1)}%`;
    if (typeof value === 'number') {
      return value >= 1000 ? `${(value / 1000).toFixed(1)}k` : value.toFixed(1);
    }
    return String(value);
  };

  const formatTimestamp = (ts: string): string => {
    try {
      return format(parseISO(ts), 'HH:mm');
    } catch {
      return ts;
    }
  };

  if (!chartData.length) {
    return (
      <div className={cn('rounded-lg border border-border bg-card p-5', className)}>
        <h3 className="text-sm font-medium text-foreground mb-4">{title}</h3>
        <div className="flex items-center justify-center h-[200px] text-sm text-muted-foreground">
          No data available
        </div>
      </div>
    );
  }

  return (
    <div className={cn('rounded-lg border border-border bg-card p-5', className)}>
      <h3 className="text-sm font-medium text-foreground mb-4">{title}</h3>
      <ResponsiveContainer width="100%" height={height}>
        <AreaChart data={chartData} margin={{ top: 5, right: 5, left: 0, bottom: 5 }}>
          <defs>
            {series.map((_, idx) => (
              <linearGradient key={idx} id={`gradient_${idx}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={CHART_COLORS[idx % CHART_COLORS.length].fill} stopOpacity={0.15} />
                <stop offset="100%" stopColor={CHART_COLORS[idx % CHART_COLORS.length].fill} stopOpacity={0.01} />
              </linearGradient>
            ))}
          </defs>

          <CartesianGrid
            strokeDasharray="3 3"
            stroke="hsl(var(--border))"
            vertical={false}
          />

          <XAxis
            dataKey="timestamp"
            tickFormatter={formatTimestamp}
            stroke="hsl(var(--muted-foreground))"
            fontSize={11}
            tickLine={false}
            axisLine={false}
            minTickGap={40}
          />

          <YAxis
            tickFormatter={formatValue}
            stroke="hsl(var(--muted-foreground))"
            fontSize={11}
            tickLine={false}
            axisLine={false}
            width={60}
          />

          <Tooltip
            content={({ active, payload, label }) => {
              if (!active || !payload) return null;
              return (
                <div className="rounded-lg border border-border bg-popover px-3 py-2 shadow-xl">
                  <p className="text-xs text-muted-foreground mb-1.5">
                    {label ? format(parseISO(label as string), 'MMM d, HH:mm:ss') : ''}
                  </p>
                  {payload.map((entry, idx) => (
                    <div key={idx} className="flex items-center gap-2 text-sm">
                      <span
                        className="inline-block w-2 h-2 rounded-full"
                        style={{ backgroundColor: entry.color }}
                      />
                      <span className="text-muted-foreground">
                        {series[idx]?.label || series[idx]?.name || `Series ${idx + 1}`}:
                      </span>
                      <span className="font-medium text-foreground tabular-nums">
                        {formatValue(entry.value as number)}
                      </span>
                    </div>
                  ))}
                </div>
              );
            }}
          />

          {series.length > 1 && (
            <Legend
              content={({ payload }) => (
                <div className="flex items-center justify-center gap-4 mt-2">
                  {payload?.map((entry, idx) => (
                    <div key={idx} className="flex items-center gap-1.5 text-xs text-muted-foreground">
                      <span
                        className="inline-block w-2.5 h-0.5 rounded-full"
                        style={{ backgroundColor: entry.color }}
                      />
                      {series[idx]?.label || series[idx]?.name || `Series ${idx + 1}`}
                    </div>
                  ))}
                </div>
              )}
            />
          )}

          {series.map((_, idx) => (
            <Area
              key={idx}
              type="monotone"
              dataKey={`series_${idx}`}
              stroke={CHART_COLORS[idx % CHART_COLORS.length].stroke}
              strokeWidth={1.5}
              fill={`url(#gradient_${idx})`}
              dot={false}
              activeDot={{
                r: 3,
                strokeWidth: 0,
                fill: CHART_COLORS[idx % CHART_COLORS.length].stroke,
              }}
            />
          ))}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
