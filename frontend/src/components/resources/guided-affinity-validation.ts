import type { ManifestPath } from "./guided-resource-model";

type Row = Record<string, unknown>;
const record = (value: unknown): Row =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Row)
    : {};
const rows = (value: unknown): Row[] =>
  Array.isArray(value) ? value.map(record) : [];

/** Fast feedback for guided fields; API-server dry-run remains authoritative. */
export function validateAffinity(
  affinity: unknown,
  path: ManifestPath,
): Record<string, string> {
  const errors: Record<string, string> = {};
  const error = (at: ManifestPath, message: string) => {
    errors[at.join(".")] = message;
  };
  const weight = (item: Row, at: ManifestPath) => {
    if (
      !Number.isInteger(item.weight) ||
      Number(item.weight) < 1 ||
      Number(item.weight) > 100
    )
      error([...at, "weight"], "Weight must be an integer from 1 to 100.");
  };
  const expressions = (value: unknown, at: ManifestPath, node: boolean) => {
    rows(value).forEach((item, index) => {
      const entry = [...at, index];
      if (typeof item.key !== "string" || !item.key.trim())
        error([...entry, "key"], "A label key is required.");
      const operators = [
        "In",
        "NotIn",
        "Exists",
        "DoesNotExist",
        ...(node ? ["Gt", "Lt"] : []),
      ];
      if (!operators.includes(String(item.operator)))
        error([...entry, "operator"], "Select a supported match operator.");
      const values = Array.isArray(item.values) ? item.values : [];
      if (
        ["In", "NotIn"].includes(String(item.operator)) &&
        (!values.length || values.some((value) => typeof value !== "string"))
      )
        error([...entry, "values"], "In and NotIn require string values.");
      if (
        ["Exists", "DoesNotExist"].includes(String(item.operator)) &&
        values.length
      )
        error(
          [...entry, "values"],
          "Exists and DoesNotExist must not have values.",
        );
      if (
        ["Gt", "Lt"].includes(String(item.operator)) &&
        (values.length !== 1 ||
          typeof values[0] !== "string" ||
          !/^-?\d+$/.test(values[0]))
      )
        error(
          [...entry, "values"],
          "Gt and Lt require exactly one integer value.",
        );
    });
  };
  const node = record(record(affinity).nodeAffinity);
  const nodePath = [...path, "nodeAffinity"];
  const required = "requiredDuringSchedulingIgnoredDuringExecution";
  const preferred = "preferredDuringSchedulingIgnoredDuringExecution";
  rows(record(node[required]).nodeSelectorTerms).forEach((item, index) =>
    expressions(
      item.matchExpressions,
      [...nodePath, required, "nodeSelectorTerms", index, "matchExpressions"],
      true,
    ),
  );
  rows(node[preferred]).forEach((item, index) => {
    const at = [...nodePath, preferred, index];
    weight(item, at);
    expressions(
      record(item.preference).matchExpressions,
      [...at, "preference", "matchExpressions"],
      true,
    );
  });
  const podTerm = (term: Row, at: ManifestPath) => {
    if (typeof term.topologyKey !== "string" || !term.topologyKey.trim())
      error([...at, "topologyKey"], "A topology key is required.");
    for (const selector of ["labelSelector", "namespaceSelector"])
      expressions(
        record(term[selector]).matchExpressions,
        [...at, selector, "matchExpressions"],
        false,
      );
  };
  for (const kind of ["podAffinity", "podAntiAffinity"]) {
    const pod = record(record(affinity)[kind]);
    rows(pod[required]).forEach((term, index) =>
      podTerm(term, [...path, kind, required, index]),
    );
    rows(pod[preferred]).forEach((item, index) => {
      const at = [...path, kind, preferred, index];
      weight(item, at);
      podTerm(record(item.podAffinityTerm), [...at, "podAffinityTerm"]);
    });
  }
  return errors;
}
