import {
  getCatalogRecommendationsPopular,
  getCatalogRecommendationsSimilarByChartId,
} from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { ChartScore } from "./catalog";
type ChartRecommendationWire =
  OpenAPIComponents["schemas"]["ChartRecommendation"];
function mapChartScore(wire: ChartRecommendationWire): ChartScore {
  return {
    chartId: wire.chart_id,
    ratingCount: wire.rating_count,
    avgStars: wire.avg_stars,
    bayesianScore: wire.bayesian_score,
    weight: wire.weight,
  };
}
export async function getPopularCharts(limit = 6): Promise<ChartScore[]> {
  const response = await getCatalogRecommendationsPopular({ query: { limit } });
  return (response.data ?? []).map(mapChartScore);
}
export async function getSimilarCharts(
  chartId: string,
  limit = 5,
): Promise<ChartScore[]> {
  const response = await getCatalogRecommendationsSimilarByChartId({
    path: { chart_id: chartId },
    query: { limit },
  });
  return (response.data ?? []).map(mapChartScore);
}
