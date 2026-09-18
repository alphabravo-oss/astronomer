import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { HelmChart, HelmChartCategory } from "@/types";
import type { HelmRepository } from "@/types";
import {
  catalogSourcePresentation,
  type CatalogSourcePresentation,
} from "@/lib/catalogs/source";

export type CatalogSupportTier = "Astronomer" | "Upstream" | "Experimental";
type CatalogWire =
  OpenAPIComponents["schemas"]["CatalogApplicationPresentation"];

export interface CatalogPresentation {
  slug: string;
  displayName: string;
  description: string;
  iconUrl: string;
  category: HelmChartCategory;
  keywords: string[];
  supportTier: CatalogSupportTier;
  featured: boolean;
  privileged: boolean;
  defaultEnabled: boolean;
  resourceProfile: "Small" | "Medium" | "Large";
  storage: string;
  compatibility: Record<string, unknown>;
  lifecycle: Record<string, boolean>;
  documentationUrl: string;
  catalogDigest: string;
  verificationStatus: CatalogWire["verification_status"];
  verificationIdentity: string;
  revoked: boolean;
  repositoryName: string;
  chartName: string;
}

export type CuratedHelmChart = HelmChart & {
  catalogPresentation?: CatalogPresentation;
  catalogSource: CatalogSourcePresentation;
};

export type CatalogPresentationIndex = ReadonlyMap<string, CatalogPresentation>;

function titleCase(value: string): string {
  return value.length ? value[0].toUpperCase() + value.slice(1) : value;
}

function presentationFromWire(wire: CatalogWire): CatalogPresentation {
  const resources = wire.resources as { profile?: string };
  const storage = wire.storage as {
    required?: boolean;
    recommended?: string;
    hostStorage?: boolean;
  };
  const presentation = wire.presentation as { keywords?: string[] };
  const profile = titleCase(resources.profile || "medium") as
    "Small" | "Medium" | "Large";
  const storageLabel = storage.hostStorage
    ? "Uses host storage"
    : storage.required
      ? storage.recommended
        ? `${storage.recommended} recommended`
        : "Required"
      : "None";
  return {
    slug: wire.slug,
    displayName: wire.display_name,
    description: wire.description || "",
    iconUrl: wire.icon_url || "",
    category: wire.category as HelmChartCategory,
    keywords: presentation.keywords || [],
    supportTier: titleCase(wire.support_tier) as CatalogSupportTier,
    featured: wire.featured,
    privileged: wire.privileged,
    defaultEnabled: wire.default_enabled,
    resourceProfile: profile,
    storage: storageLabel,
    compatibility: wire.compatibility,
    lifecycle: wire.lifecycle,
    documentationUrl: wire.documentation_url || "",
    catalogDigest: wire.catalog_digest,
    verificationStatus: wire.verification_status,
    verificationIdentity: wire.verification_identity,
    revoked: wire.revoked,
    repositoryName: wire.repo_name,
    chartName: wire.chart_name,
  };
}

export function buildCatalogPresentationIndex(
  applications: CatalogWire[] | undefined,
): CatalogPresentationIndex {
  const index = new Map<string, CatalogPresentation>();
  for (const wire of applications || []) {
    const presentation = presentationFromWire(wire);
    index.set(
      `${presentation.repositoryName}/${presentation.chartName}`,
      presentation,
    );
  }
  return index;
}

export function decorateCatalogChart(
  chart: HelmChart,
  presentations: CatalogPresentationIndex,
  repository?: HelmRepository,
): CuratedHelmChart {
  const presentation = presentations.get(
    `${chart.repositoryName}/${chart.name}`,
  );
  const source = catalogSourcePresentation(
    repository,
    Boolean(presentation),
    {
      repositoryId: chart.repositoryId,
      repositoryName: chart.repositoryName,
    },
    presentation?.supportTier === "Astronomer" &&
      presentation.slug === "constellation",
  );
  if (!presentation) return { ...chart, catalogSource: source };
  return {
    ...chart,
    displayName: presentation.displayName,
    description: presentation.description,
    iconUrl: presentation.iconUrl,
    category: presentation.category,
    keywords:
      presentation.keywords.length > 0 ? presentation.keywords : chart.keywords,
    catalogPresentation: presentation,
    catalogSource: source,
  };
}
