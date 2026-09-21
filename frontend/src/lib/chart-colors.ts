/**
 * Shared chart color tokens. Charts must reference the semantic --status-*
 * CSS variables (see styles/globals.css) instead of hard-coded hex/rgb
 * literals, so a chart's palette follows the same light/dark theme as the
 * rest of the app. Moved out of metrics-chart.tsx so other charts (e.g. the
 * image-scans severity trend) share the same series palette instead of
 * re-declaring their own hex constants.
 */

/** Ordered palette for multi-series line/area charts (MetricsChart). */
export const SERIES = [
  { stroke: "hsl(var(--status-info))", fill: "hsl(var(--status-info))" },
  { stroke: "hsl(var(--status-pending))", fill: "hsl(var(--status-pending))" },
  { stroke: "hsl(var(--status-success))", fill: "hsl(var(--status-success))" },
  { stroke: "hsl(var(--status-warning))", fill: "hsl(var(--status-warning))" },
  { stroke: "hsl(var(--status-error))", fill: "hsl(var(--status-error))" },
];

/** Vulnerability/finding severity palette (image-scans trend, security views). */
export const SEVERITY = {
  critical: "hsl(var(--status-error))",
  high: "hsl(var(--status-high))",
  medium: "hsl(var(--status-warning))",
  low: "hsl(var(--status-info))",
};
