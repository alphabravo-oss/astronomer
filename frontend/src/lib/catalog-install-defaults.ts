import {
  dumpHelmValuesYAML,
  parseHelmValuesYAML,
  setValueAtPath,
  type HelmValuesObject,
} from "@/lib/helm-values-schema";

// These are compatibility defaults for a fresh install, kept deliberately
// small and visible in the values editor. They prevent a supported App from
// competing with components Astronomer already installed on the cluster while
// leaving every switch available to an operator who intentionally wants a
// separate stack.
const INSTALL_DEFAULT_OVERRIDES: Record<
  string,
  Array<{ path: string[]; value: unknown }>
> = {
  "kube-prometheus-stack": [
    { path: ["prometheusOperator", "enabled"], value: false },
    { path: ["nodeExporter", "enabled"], value: false },
    { path: ["kubeStateMetrics", "enabled"], value: false },
  ],
};

export function catalogInstallDefaultValues(
  chartName: string,
  upstreamValues: string,
): string {
  let parsed: HelmValuesObject | null;
  try {
    parsed = parseHelmValuesYAML(upstreamValues);
  } catch {
    return upstreamValues;
  }
  if (parsed == null) return upstreamValues;
  let values: HelmValuesObject = parsed;
  for (const override of INSTALL_DEFAULT_OVERRIDES[chartName.toLowerCase()] ??
    []) {
    values = setValueAtPath(values, override.path, override.value);
  }
  return dumpHelmValuesYAML(values);
}
