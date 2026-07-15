import { render, waitFor } from '@testing-library/react';
import { WidgetGrid } from './widget-grid';
import type { RenderedWidget } from '@/lib/api/dashboards';

// Regression: WidgetGrid must not re-fetch every widget just because the parent
// re-rendered and handed it a new `fetcher` function identity. The cluster
// detail page re-renders on every SSE metrics tick, so an identity-keyed effect
// would fan out to the /render endpoint (→ Prometheus/Grafana per widget) on
// every tick — a request storm far exceeding each widget's refreshSeconds.
describe('WidgetGrid — stable fetch scheduling', () => {
  it('does not re-run the fetch when the parent passes a new fetcher identity', async () => {
    const firstFetcher = vi.fn<() => Promise<RenderedWidget[]>>().mockResolvedValue([]);

    const { rerender } = render(<WidgetGrid fetcher={firstFetcher} />);
    await waitFor(() => expect(firstFetcher).toHaveBeenCalledTimes(1));

    // Parent re-renders three times, each time creating a brand-new fetcher
    // function (as happens with an inline `() => renderForCluster(id)`).
    for (let i = 0; i < 3; i++) {
      const nextFetcher = vi.fn<() => Promise<RenderedWidget[]>>().mockResolvedValue([]);
      rerender(<WidgetGrid fetcher={nextFetcher} />);
      // Give any (incorrectly re-triggered) effect a chance to fire.
      await Promise.resolve();
      // The new fetcher identity must NOT cause an extra fetch.
      expect(nextFetcher).not.toHaveBeenCalled();
    }

    // Still exactly one fetch across all the parent re-renders.
    expect(firstFetcher).toHaveBeenCalledTimes(1);
  });
});
