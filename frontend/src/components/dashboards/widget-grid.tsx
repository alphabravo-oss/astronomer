/**
 * WidgetGrid — renders a server-side widget grid (migration 058).
 *
 * The grid layout uses CSS grid with 12 columns; each widget's `grid`
 * (x, y, w, h) maps to (col-start, row-start, col-span, row-span).
 * Server has already done the spec → resolved-spec templating + the
 * sparkline/stat fetching, so this component is pure presentation —
 * it polls the render endpoint at the widget's `refreshSeconds` and
 * swaps the SVG / stat value in place.
 *
 * Per-widget render branches:
 *   - prom_sparkline: server-rendered SVG injected via
 *     dangerouslySetInnerHTML. The SVG is plain XML built by the Go
 *     handler — no user-supplied content reaches the DOM, so the
 *     usage is safe. We still gate the unsafe innerHTML behind a
 *     widget_type check (the data envelope's `sparklineSvg` is only
 *     non-empty for this type).
 *   - prom_stat: numeric scalar formatted per spec.format + spec.unit.
 *   - grafana_panel: sandboxed iframe at the templated URL.
 *   - url_iframe: same as grafana_panel but no panel-id postfix.
 */
'use client';

import { useEffect, useMemo, useState } from 'react';
import { Loader2, AlertCircle } from 'lucide-react';
import type { RenderedWidget } from '@/lib/api/dashboards';

export type WidgetFetcher = () => Promise<RenderedWidget[]>;

export function WidgetGrid({ fetcher, emptyHint }: { fetcher: WidgetFetcher; emptyHint?: string }) {
  const [widgets, setWidgets] = useState<RenderedWidget[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let mounted = true;
    let timers: ReturnType<typeof setTimeout>[] = [];

    const load = async () => {
      try {
        const list = await fetcher();
        if (!mounted) return;
        setWidgets(list);
        setError(null);
      } catch (e) {
        if (!mounted) return;
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (mounted) setLoading(false);
      }
    };

    void load();

    // Refresh on a per-widget cadence. We schedule one timer per widget
    // based on its refreshSeconds; consecutive client polls inside the
    // server's 30s cache window share an upstream fetch.
    const schedule = () => {
      timers.forEach(clearTimeout);
      timers = [];
      if (!widgets) return;
      widgets.forEach((w) => {
        const sec = Math.max(5, w.refreshSeconds || 60);
        timers.push(setTimeout(load, sec * 1000));
      });
    };
    schedule();

    return () => {
      mounted = false;
      timers.forEach(clearTimeout);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fetcher]);

  if (loading && !widgets) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground py-6">
        <Loader2 className="h-4 w-4 animate-spin" />
        <span>Loading widgets...</span>
      </div>
    );
  }
  if (error) {
    return (
      <div className="flex items-center gap-2 text-sm text-red-600 py-6">
        <AlertCircle className="h-4 w-4" />
        <span>Widgets: {error}</span>
      </div>
    );
  }
  if (!widgets || widgets.length === 0) {
    return (
      <div className="text-sm text-muted-foreground py-4">
        {emptyHint ?? 'No widgets configured. Add one in Settings → Widgets.'}
      </div>
    );
  }

  return (
    <div className="grid grid-cols-12 gap-3 auto-rows-[80px]">
      {widgets.map((w) => (
        <div
          key={w.id}
          className="border border-border rounded-lg bg-card p-3 overflow-hidden flex flex-col"
          style={{
            gridColumn: `span ${Math.min(12, Math.max(1, w.grid.w))}`,
            gridRow: `span ${Math.max(1, w.grid.h)}`,
          }}
        >
          <div className="text-xs font-medium text-muted-foreground uppercase tracking-wide mb-1 truncate">
            {w.name}
          </div>
          <WidgetBody widget={w} />
        </div>
      ))}
    </div>
  );
}

function WidgetBody({ widget }: { widget: RenderedWidget }) {
  const data = widget.data ?? {};
  if (data.error) {
    return (
      <div className="flex-1 text-xs text-amber-600 flex items-center gap-1">
        <AlertCircle className="h-3 w-3" />
        <span className="truncate" title={data.error}>{data.error}</span>
      </div>
    );
  }
  switch (widget.widgetType) {
    case 'prom_sparkline': {
      const svg = data.sparklineSvg ?? data.sparkline_svg ?? '';
      return (
        <div className="flex-1 flex items-center text-foreground/80">
          {svg ? (
            <div
              className="w-full"
              // SVG is server-built from a fixed renderer; no operator-
              // supplied markup reaches this innerHTML site.
              dangerouslySetInnerHTML={{ __html: svg }}
            />
          ) : (
            <span className="text-xs text-muted-foreground">No data</span>
          )}
        </div>
      );
    }
    case 'prom_stat': {
      const ok = data.statOk ?? data.stat_ok ?? false;
      const value = data.statValue ?? data.stat_value ?? 0;
      const unit = data.statUnit ?? data.stat_unit ?? '';
      const format = data.statFormat ?? data.stat_format ?? '';
      return (
        <div className="flex-1 flex items-center">
          <div className="text-2xl font-semibold text-foreground">
            {ok ? formatStat(value, format) : '—'}
            {ok && unit ? <span className="text-sm text-muted-foreground ml-1">{unit}</span> : null}
          </div>
        </div>
      );
    }
    case 'grafana_panel': {
      const url = grafanaIframeURL(widget.specResolved);
      if (!url) return <div className="text-xs text-muted-foreground">Missing base_url / dashboard_uid</div>;
      return (
        <iframe
          className="flex-1 w-full h-full border-0"
          src={url}
          sandbox="allow-same-origin allow-scripts"
          loading="lazy"
          title={widget.name}
        />
      );
    }
    case 'url_iframe': {
      const url = (widget.specResolved as any)?.url ?? '';
      if (!url) return <div className="text-xs text-muted-foreground">Missing url</div>;
      return (
        <iframe
          className="flex-1 w-full h-full border-0"
          src={url}
          sandbox="allow-same-origin allow-scripts"
          loading="lazy"
          title={widget.name}
        />
      );
    }
    default:
      return <div className="text-xs text-muted-foreground">Unsupported widget type</div>;
  }
}

function formatStat(value: number, format: string): string {
  if (!format) return String(value);
  // Tiny printf-ish: support `.<n>f` for fixed precision.
  const m = format.match(/^\.(\d+)f$/);
  if (m) return value.toFixed(parseInt(m[1], 10));
  return String(value);
}

function grafanaIframeURL(spec: any): string {
  if (!spec || !spec.base_url || !spec.dashboard_uid) return '';
  const base = String(spec.base_url).replace(/\/$/, '');
  const path = `/d-solo/${encodeURIComponent(spec.dashboard_uid)}`;
  const qs = new URLSearchParams();
  if (spec.panel_id !== undefined) qs.set('panelId', String(spec.panel_id));
  if (spec.vars && typeof spec.vars === 'object') {
    for (const [k, v] of Object.entries(spec.vars as Record<string, string>)) {
      qs.set(`var-${k}`, String(v));
    }
  }
  // Theme dark fits the SPA shell; operators can override via vars.
  if (!qs.has('theme')) qs.set('theme', 'dark');
  return `${base}${path}?${qs.toString()}`;
}
