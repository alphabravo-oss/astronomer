import type { HelmRepository } from "@/types";

export type CatalogSourceFamily =
  | "first-party"
  | "curated"
  | "community"
  | "custom";

export interface CatalogSourcePresentation {
  family: CatalogSourceFamily;
  familyLabel: string;
  repositoryId: string;
  repositoryName: string;
  label: string;
  foreground: string;
  background: string;
  border: string;
}

const CURATED_COLORS = {
  foreground: "hsl(252 72% 58%)",
  background: "hsl(252 72% 58% / 0.12)",
  border: "hsl(252 72% 58% / 0.35)",
};

const FIRST_PARTY_COLORS = {
  foreground: "hsl(168 72% 34%)",
  background: "hsl(168 72% 42% / 0.13)",
  border: "hsl(168 72% 38% / 0.4)",
};

const COMMUNITY_COLORS = {
  foreground: "hsl(210 16% 48%)",
  background: "hsl(210 16% 48% / 0.12)",
  border: "hsl(210 16% 48% / 0.32)",
};

function stableHue(identity: string): number {
  let hash = 2166136261;
  for (let index = 0; index < identity.length; index += 1) {
    hash ^= identity.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return Math.abs(hash) % 360;
}

function customColors(identity: string) {
  const hue = stableHue(identity);
  return {
    foreground: `hsl(${hue} 62% 43%)`,
    background: `hsl(${hue} 68% 48% / 0.12)`,
    border: `hsl(${hue} 62% 48% / 0.38)`,
  };
}

export function catalogSourcePresentation(
  repository: HelmRepository | undefined,
  curated: boolean,
  fallback: { repositoryId: string; repositoryName?: string },
  firstParty = false,
): CatalogSourcePresentation {
  const repositoryId = repository?.id || fallback.repositoryId;
  const repositoryName =
    repository?.name || fallback.repositoryName || "Unknown repository";
  if (firstParty) {
    return {
      family: "first-party",
      familyLabel: "Astronomer First Party",
      repositoryId,
      repositoryName,
      label: "Astronomer First Party",
      ...FIRST_PARTY_COLORS,
    };
  }
  if (curated) {
    return {
      family: "curated",
      familyLabel: "Astronomer Curated",
      repositoryId,
      repositoryName,
      label: "Astronomer Curated",
      ...CURATED_COLORS,
    };
  }
  if (repository && (!repository.isDefault || Boolean(repository.ownerProjectId))) {
    return {
      family: "custom",
      familyLabel: "Custom",
      repositoryId,
      repositoryName,
      label: repositoryName,
      ...customColors(repository.id || repositoryName),
    };
  }
  return {
    family: "community",
    familyLabel: "Community",
    repositoryId,
    repositoryName,
    label: repositoryName,
    ...COMMUNITY_COLORS,
  };
}

export function catalogSourceSortWeight(family: CatalogSourceFamily): number {
  if (family === "first-party") return 0;
  if (family === "curated") return 1;
  if (family === "community") return 2;
  return 3;
}
