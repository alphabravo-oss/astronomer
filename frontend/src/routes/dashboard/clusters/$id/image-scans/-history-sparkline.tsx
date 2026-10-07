import { SEVERITY } from "@/lib/chart-colors";

// ---------------------------------------------------------------------
// HistorySparkline — minimal inline SVG trend line for the scan
// history card. Two stacked polylines (critical + high), normalised
// to the max across the window so small swings still register.
// Inline rather than dragging in recharts/visx because (a) we only
// need a 60×30 sparkline, (b) it stays consistent with the other
// metric tiles on the cluster overview (which don't ship a chart
// library either), (c) ~30 lines beats ~300KB on the bundle.
// ---------------------------------------------------------------------
export function HistorySparkline({
  points,
}: {
  points: Array<{ scannedAt: string; critical: number; high: number }>;
}) {
  if (points.length < 2) {
    return (
      <div className="h-14 flex items-center justify-center text-xs text-muted-foreground">
        At least 2 scans needed to render a trend
      </div>
    );
  }
  const width = 320;
  const height = 56;
  const padX = 4;
  const padY = 4;
  const innerW = width - padX * 2;
  const innerH = height - padY * 2;
  const maxY = Math.max(
    1,
    ...points.map((p) => p.critical),
    ...points.map((p) => p.high),
  );
  const xs = (i: number) =>
    padX + (i * innerW) / Math.max(1, points.length - 1);
  const ys = (v: number) => padY + innerH - (v / maxY) * innerH;
  const path = (key: "critical" | "high") =>
    points
      .map((p, i) => `${i === 0 ? "M" : "L"} ${xs(i)} ${ys(p[key])}`)
      .join(" ");

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className="w-full h-14"
      preserveAspectRatio="none"
      aria-label="Scan history trend"
    >
      {/* baseline */}
      <line
        x1={padX}
        x2={width - padX}
        y1={height - padY}
        y2={height - padY}
        stroke="currentColor"
        strokeOpacity={0.1}
      />
      {/* high (orange) — drawn under so critical is on top */}
      <path
        d={path("high")}
        fill="none"
        stroke={SEVERITY.high}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {/* critical (red) */}
      <path
        d={path("critical")}
        fill="none"
        stroke={SEVERITY.critical}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {/* dots on the latest point so the user knows where "now" is */}
      <circle
        cx={xs(points.length - 1)}
        cy={ys(points[points.length - 1].critical)}
        r={2.5}
        fill={SEVERITY.critical}
      />
      <circle
        cx={xs(points.length - 1)}
        cy={ys(points[points.length - 1].high)}
        r={2.5}
        fill={SEVERITY.high}
      />
    </svg>
  );
}
