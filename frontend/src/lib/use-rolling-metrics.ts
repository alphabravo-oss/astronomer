import { useEffect, useState } from 'react';
import type { MetricsSummary, MetricsSeries } from '@/types';

// A bounded live window: at a 30–60s summary poll this is ~1–2h of history.
// It's session-local — starts empty, fills as samples arrive, resets on cluster
// switch / reload. Durable long-term history is what the Prometheus stack adds.
export const ROLLING_MAX_SAMPLES = 120;

export interface RollingSample {
  t: string; // ISO timestamp
  cpu: number; // percent
  mem: number; // percent
  pods: number; // count
}

export interface RollingState {
  cid: string;
  samples: RollingSample[];
}

type SummaryScalars = Pick<MetricsSummary, 'cpuPercentage' | 'memoryPercentage' | 'podCount'>;

// appendRollingSample is the pure core: append one summary sample, resetting the
// buffer when the cluster changed, and cap to `max` (drop-oldest). Kept separate
// from the hook so the ring-buffer + reset logic is unit-testable.
export function appendRollingSample(
  prev: RollingState,
  clusterId: string,
  summary: SummaryScalars,
  t: string,
  max = ROLLING_MAX_SAMPLES,
): RollingState {
  const base = prev.cid === clusterId ? prev.samples : [];
  const sample: RollingSample = {
    t,
    cpu: summary.cpuPercentage ?? 0,
    mem: summary.memoryPercentage ?? 0,
    pods: summary.podCount ?? 0,
  };
  return { cid: clusterId, samples: [...base, sample].slice(-max) };
}

export interface RollingMetrics {
  cpu: MetricsSeries;
  mem: MetricsSeries;
  pods: MetricsSeries;
  count: number;
}

// useRollingMetrics accumulates the scalar /metrics/summary samples the page
// already polls into an in-memory time-series, so live CPU / memory / pod trends
// render without any Prometheus backend.
export function useRollingMetrics(clusterId: string, summary: SummaryScalars | undefined): RollingMetrics {
  const [state, setState] = useState<RollingState>({ cid: clusterId, samples: [] });

  useEffect(() => {
    if (!summary || !clusterId) return;
    setState((prev) => appendRollingSample(prev, clusterId, summary, new Date().toISOString()));
  }, [summary, clusterId]);

  // Guard against a one-render mismatch right after a cluster switch (state still
  // holds the previous cluster's samples until the effect runs).
  const samples = state.cid === clusterId ? state.samples : [];
  const series = (key: keyof Omit<RollingSample, 't'>, name: string, unit: string): MetricsSeries => ({
    name,
    unit,
    data: samples.map((s) => ({ timestamp: s.t, value: s[key] })),
  });

  return {
    cpu: series('cpu', 'CPU %', '%'),
    mem: series('mem', 'Memory %', '%'),
    pods: series('pods', 'Pods', ''),
    count: samples.length,
  };
}
