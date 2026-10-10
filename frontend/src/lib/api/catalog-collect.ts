import { getHelmCharts } from "./catalog";
import type { HelmChart } from "@/types/catalog";

// Explicit finite opt-in for callers that need the complete traversal. Existing
// browse enablement and project/cluster authorization are not expanded.
export async function collectHelmCharts(
  params: Omit<Parameters<typeof getHelmCharts>[0], "cursor" | "offset">,
  signal: AbortSignal,
): Promise<HelmChart[]> {
  if (!signal) throw new Error("Catalog traversal requires an owned cancellation signal");
  const bounded = new AbortController();
  const cancel = () => bounded.abort(signal.reason);
  signal.addEventListener("abort", cancel, { once: true });
  if (signal.aborted) cancel();
  const timer = setTimeout(() => bounded.abort(new DOMException("Catalog traversal exceeded 20 seconds", "TimeoutError")), 20000);
  try {
  const tokens = new Set<string>();
  const chartIds = new Set<string>();
  const out: HelmChart[] = [];
  let cursor = "";
  for (let window = 0; window < 10000; window++) {
    bounded.signal.throwIfAborted();
    const page = await getHelmCharts({ ...params, cursor }, bounded.signal);
    bounded.signal.throwIfAborted();
    const metadata = page.pagination;
    if (typeof metadata.has_more !== "boolean" || !Number.isSafeInteger(metadata.limit) || metadata.limit < 1 || metadata.limit > 200 || page.data.length > metadata.limit || metadata.offset !== out.length || out.length + page.data.length > 100000)
      throw new Error("Catalog traversal page exceeded its contract");
    for (const chart of page.data) {
      if (!chart.id || chartIds.has(chart.id)) throw new Error("Catalog traversal chart identity repeated or missing");
      chartIds.add(chart.id);
    }
    out.push(...page.data);
    if (!metadata.has_more) {
      if (metadata.next_cursor) throw new Error("Terminal catalog page carried continuation");
      return out;
    }
    const next = metadata.next_cursor;
    if (typeof next !== "string" || !next || next.length > 4096 || tokens.has(next))
      throw new Error("Catalog traversal did not advance");
    tokens.add(next);
    cursor = next;
  }
  throw new Error("Catalog traversal exceeds the 10000-window diagnostic limit");
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", cancel);
  }
}
