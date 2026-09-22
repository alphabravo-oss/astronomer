import { k8sProxy } from "./kubernetes-proxy";

export interface CRDCountType {
  group: string;
  version: string;
  plural: string;
  namespaced: boolean;
}

// Limit the entire query to 15 metadata requests, including namespace fanout.
// A type is omitted unless every selected namespace can be counted.
export function crdCountJobs(
  types: CRDCountType[],
  namespaces: readonly string[] | null,
) {
  const jobs: { key: string; path: string }[] = [];
  if (namespaces?.length === 0) return jobs;
  for (const type of types) {
    const scopes = type.namespaced && namespaces ? namespaces : [null];
    if (jobs.length + scopes.length > 15) continue;
    for (const namespace of scopes) {
      jobs.push({
        key: `crd:${type.group}/${type.plural}`,
        path: `apis/${type.group}/${type.version}/${namespace ? `namespaces/${encodeURIComponent(namespace)}/` : ""}${type.plural}?limit=1`,
      });
    }
  }
  return jobs;
}

export async function getCRDNavCounts(
  clusterId: string,
  types: CRDCountType[],
  namespaces: readonly string[] | null,
  signal?: AbortSignal,
): Promise<Record<string, number>> {
  const jobs = crdCountJobs(types, namespaces);
  const results = await Promise.allSettled(
    jobs.map(async (job) => {
      const response = await k8sProxy(
        clusterId,
        "GET",
        job.path,
        undefined,
        {
          Accept:
            "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1",
        },
        signal,
      );
      if (
        response?.kind !== "PartialObjectMetadataList" ||
        !Array.isArray(response.items)
      )
        throw new Error("Metadata counts unavailable");
      const remaining = response.metadata?.remainingItemCount;
      if (response.metadata?.continue && !Number.isSafeInteger(remaining))
        throw new Error("Incomplete resource count");
      if (
        remaining !== undefined &&
        (!Number.isSafeInteger(remaining) || remaining < 0)
      )
        throw new Error("Invalid resource count");
      return response.items.length + (remaining ?? 0);
    }),
  );
  const counts: Record<string, number> = {};
  const unavailable = new Set<string>();
  results.forEach((result, index) => {
    const key = jobs[index].key;
    if (result.status === "fulfilled")
      counts[key] = (counts[key] ?? 0) + result.value;
    else unavailable.add(key);
  });
  for (const key of unavailable) delete counts[key];
  return counts;
}
