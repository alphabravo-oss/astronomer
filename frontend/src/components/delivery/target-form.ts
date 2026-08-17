import type {
  LabelExpression,
  LabelOperator,
  Placement,
  PlacementRequest,
} from "@/lib/api/delivery";

export function placementFromForm(
  form: FormData,
  allClusters: boolean,
): PlacementRequest {
  const ids = (name: string) =>
    String(form.get(name) ?? "")
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
  if (allClusters) {
    return {
      all_clusters: true,
      exclude_cluster_ids: ids("exclude_ids"),
    };
  }
  const labels = Object.fromEntries(
    String(form.get("labels") ?? "")
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
      .map((line) => {
        const separator = line.indexOf("=");
        if (separator < 1)
          throw new Error(`Invalid label “${line}”; expected key=value.`);
        return [
          line.slice(0, separator).trim(),
          line.slice(separator + 1).trim(),
        ];
      }),
  );
  const expressions: LabelExpression[] = String(form.get("expressions") ?? "")
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const match = line.match(
        /^(\S+)\s+(In|NotIn|Exists|DoesNotExist)(?:\s+(.+))?$/,
      );
      if (!match) throw new Error(`Invalid expression “${line}”.`);
      const operator = match[2] as LabelOperator;
      const values = match[3]
        ?.split(",")
        .map((value) => value.trim())
        .filter(Boolean);
      if ((operator === "In" || operator === "NotIn") && !values?.length) {
        throw new Error(`${operator} expression “${line}” requires values.`);
      }
      if ((operator === "Exists" || operator === "DoesNotExist") && values?.length) {
        throw new Error(`${operator} expression “${line}” cannot include values.`);
      }
      return { key: match[1], operator, ...(values?.length ? { values } : {}) };
    });
  return {
    all_clusters: false,
    cluster_ids: ids("cluster_ids"),
    cluster_group_ids: ids("group_ids"),
    match_labels: labels,
    match_expressions: expressions.map((value) => ({
      key: value.key,
      operator: value.operator,
      values: value.values,
    })),
    exclude_cluster_ids: ids("exclude_ids"),
  };
}

export function placementFormDefaults(placement: Placement) {
  return {
    allClusters: placement.allClusters,
    clusterIds: placement.clusterIds?.join(", ") ?? "",
    groupIds: placement.clusterGroupIds?.join(", ") ?? "",
    labels: Object.entries(placement.matchLabels ?? {})
      .map(([key, value]) => `${key}=${value}`)
      .join("\n"),
    expressions: (placement.matchExpressions ?? [])
      .map((expression) =>
        [expression.key, expression.operator, expression.values?.join(",")]
          .filter(Boolean)
          .join(" "),
      )
      .join("\n"),
    excludeIds: placement.excludeClusterIds?.join(", ") ?? "",
  };
}

export function placementHasSelector(placement: PlacementRequest): boolean {
  return Boolean(
    placement.all_clusters ||
      placement.cluster_ids?.length ||
      placement.cluster_group_ids?.length ||
      Object.keys(placement.match_labels ?? {}).length ||
      placement.match_expressions?.length,
  );
}
