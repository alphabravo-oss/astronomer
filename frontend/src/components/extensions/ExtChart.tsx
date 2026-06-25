'use client';

// §Schema Tier-1 — chart renderer. Series rows -> first-party SVG bars/areas.
// Runs no third-party JS and no charting library: geometry comes from the pure
// buildChartSeries/chartMax helpers and is painted as <rect>/<polyline> with the
// x labels as text nodes. A hostile manifest can only influence bar heights and
// label text — never markup.

import { cn } from '@/lib/utils';
import { buildChartSeries, chartMax, formatValue, type ProxyRow } from './declarative';
import type { ChartSpec } from '@/lib/api/extensions';

export interface ExtChartProps {
  rows: ProxyRow[];
  spec: ChartSpec;
  emptyText?: string;
}

// Fixed palette indexed by series position. Closed set so the manifest cannot
// name an arbitrary color string.
const SERIES_COLORS = [
  'var(--primary)',
  'var(--status-info)',
  'var(--status-warning)',
  'var(--status-success)',
];

const CHART_H = 160;
const CHART_W = 480;

export function ExtChart({ rows, spec, emptyText }: ExtChartProps) {
  const points = buildChartSeries(rows, spec);
  const max = chartMax(points);

  if (points.length === 0) {
    return (
      <p className="px-3 py-6 text-center text-sm text-muted-foreground">
        {emptyText || 'No data'}
      </p>
    );
  }

  const colCount = points.length;
  const colW = CHART_W / colCount;
  const denom = max > 0 ? max : 1;

  return (
    <div className="w-full overflow-x-auto">
      <svg
        role="img"
        aria-label={`${spec.type} chart`}
        viewBox={`0 0 ${CHART_W} ${CHART_H + 24}`}
        className="h-48 w-full"
        preserveAspectRatio="none"
      >
        {points.map((p, i) =>
          p.values.map((v, s) => {
            const h = (v / denom) * CHART_H;
            const groupW = colW / Math.max(1, spec.y.length);
            const x = i * colW + s * groupW;
            const color = SERIES_COLORS[s % SERIES_COLORS.length];
            if (spec.type === 'bar') {
              return (
                <rect
                  key={`${i}-${s}`}
                  x={x + 2}
                  y={CHART_H - h}
                  width={Math.max(1, groupW - 4)}
                  height={Math.max(0, h)}
                  fill={color}
                  rx={1}
                />
              );
            }
            // line/area: draw a marker per point; the connecting polyline below.
            return (
              <circle key={`${i}-${s}`} cx={x + groupW / 2} cy={CHART_H - h} r={2} fill={color} />
            );
          }),
        )}
        {(spec.type === 'line' || spec.type === 'area') &&
          spec.y.map((_, s) => {
            const color = SERIES_COLORS[s % SERIES_COLORS.length];
            const coords = points
              .map((p, i) => {
                const h = (p.values[s] / denom) * CHART_H;
                return `${i * colW + colW / 2},${CHART_H - h}`;
              })
              .join(' ');
            return (
              <polyline
                key={`series-${s}`}
                points={coords}
                fill="none"
                stroke={color}
                strokeWidth={1.5}
              />
            );
          })}
        {points.map((p, i) => (
          <text
            key={`label-${i}`}
            x={i * colW + colW / 2}
            y={CHART_H + 16}
            textAnchor="middle"
            className={cn('fill-muted-foreground text-[10px]')}
          >
            {p.x}
          </text>
        ))}
      </svg>
      <div className="mt-1 flex flex-wrap gap-3 px-2">
        {spec.y.map((y, s) => (
          <span key={y} className="flex items-center gap-1 text-xs text-muted-foreground">
            <span
              className="inline-block h-2 w-2 rounded-sm"
              style={{ background: SERIES_COLORS[s % SERIES_COLORS.length] }}
            />
            {formatValue(y, 'text')}
          </span>
        ))}
      </div>
    </div>
  );
}
