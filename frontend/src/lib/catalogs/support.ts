import type { OpenAPIComponents } from "@/types/openapi.generated";
import { offeringSupport } from "./assurance";

type Presentation =
  OpenAPIComponents["schemas"]["CatalogApplicationPresentation"];

/** Repository identity is required: a matching chart name alone proves nothing. */
export function supportForChart(
  rows: Presentation[] | undefined,
  chart: string,
  repository?: string,
  version?: string,
) {
  const matches =
    Array.isArray(rows) && repository
      ? rows.filter(
          (row) =>
            row.chart_name === chart &&
            (row.repo_name === repository || row.repo_url === repository),
        )
      : [];
  const row = matches.length === 1 ? matches[0] : undefined;
  return offeringSupport(
    row
      ? {
          tier: row.support_tier,
          artifact: row.artifact,
          verification: row.verification_status,
          revoked: row.revoked,
          version,
        }
      : undefined,
  );
}
