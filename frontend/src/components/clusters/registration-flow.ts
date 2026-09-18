export interface RegistrationSearch {
  clusterId?: string;
  tab?: string;
}

export function parseRegistrationSearch(
  search: Record<string, unknown>,
): RegistrationSearch {
  return {
    clusterId:
      typeof search.clusterId === "string" && search.clusterId.length > 0
        ? search.clusterId
        : undefined,
    tab: typeof search.tab === "string" ? search.tab : undefined,
  };
}

export function registrationSearch(clusterId?: string): RegistrationSearch {
  return clusterId ? { clusterId } : {};
}
