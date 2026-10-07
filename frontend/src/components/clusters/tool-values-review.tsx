import { buildYamlDiff } from "@/components/ui/yaml-apply-preview";
export function ToolValuesReview({
  baseline,
  effective,
  loading,
  error,
  checks,
  isUpgrade,
}: {
  baseline: Array<{
    chartName: string;
    chartVersion: string;
    releaseName?: string;
    namespace: string;
    valuesYaml: string;
  }>;
  effective: Array<{
    chartName: string;
    chartVersion: string;
    releaseName?: string;
    namespace: string;
    valuesYaml: string;
  }>;
  loading: boolean;
  error: Error | null;
  checks: Array<{
    code: string;
    status: "pass" | "warn" | "block";
    message: string;
  }>;
  isUpgrade: boolean;
}) {
  if (loading) {
    return (
      <p className="text-sm text-muted-foreground">
        Building server-validated preview…
      </p>
    );
  }
  if (error) {
    return (
      <p role="alert" className="text-sm text-status-error">
        Could not build the effective values preview: {error.message}
      </p>
    );
  }
  return (
    <div className="space-y-5">
      <p className="text-xs text-muted-foreground">
        {isUpgrade
          ? "Changes from the saved installation to the proposed server-validated values."
          : "Changes from the selected preset to the proposed server-validated values."}{" "}
        Each release is shown separately in installation order.
      </p>
      {checks.length > 0 && (
        <section aria-label="Preflight checks" className="space-y-2">
          <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Preflight
          </h3>
          {checks.map((check) => (
            <div
              key={check.code}
              className={`rounded-md border px-3 py-2 text-xs ${
                check.status === "block"
                  ? "border-status-error/30 bg-status-error/5 text-status-error"
                  : check.status === "warn"
                    ? "border-status-warning/30 bg-status-warning/5 text-status-warning"
                    : "border-status-success/30 bg-status-success/5 text-status-success"
              }`}
            >
              <span className="font-semibold capitalize">{check.status}</span>
              {" — "}
              {check.message}
            </div>
          ))}
        </section>
      )}
      {effective.map((chart, index) => {
        const before = baseline[index]?.valuesYaml ?? "";
        const diff = buildYamlDiff(before, chart.valuesYaml);
        return (
          <section
            key={`${chart.namespace}/${chart.releaseName ?? chart.chartName}`}
            className="overflow-hidden rounded-lg border border-border"
          >
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-muted/30 px-3 py-2">
              <div>
                <h3 className="text-sm font-medium text-foreground">
                  {index + 1}. {chart.releaseName ?? chart.chartName}
                </h3>
                <p className="text-[11px] text-muted-foreground">
                  {chart.chartName}@{chart.chartVersion} · {chart.namespace}
                </p>
              </div>
              <span className="text-xs tabular-nums text-muted-foreground">
                +{diff.added} / -{diff.removed}
              </span>
            </div>
            <pre className="max-h-64 overflow-auto p-3 text-xs leading-5">
              {diff.lines.map((line) => (
                <div
                  key={line.key}
                  className={`min-w-max font-mono ${
                    line.type === "add"
                      ? "bg-status-success/10 text-status-success"
                      : line.type === "remove"
                        ? "bg-status-error/10 text-status-error"
                        : "text-muted-foreground"
                  }`}
                >
                  <span className="inline-block w-4 select-none">
                    {line.type === "add"
                      ? "+"
                      : line.type === "remove"
                        ? "-"
                        : " "}
                  </span>
                  {line.text}
                </div>
              ))}
            </pre>
          </section>
        );
      })}
    </div>
  );
}
