import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import type { OpenAPIComponents } from "@/types/openapi.generated";
export function CatalogApplicationReview({
  preview,
  loading,
  error,
  chartName,
  version,
  namespace,
  releaseName,
}: {
  preview?: OpenAPIComponents["schemas"]["CatalogInstallationPreview"];
  loading: boolean;
  error: Error | null;
  chartName: string;
  version: string;
  namespace: string;
  releaseName: string;
}) {
  if (loading)
    return (
      <p className="text-sm text-muted-foreground">
        Validating trust, compatibility, access and configuration…
      </p>
    );
  if (error)
    return (
      <p role="alert" className="text-sm text-status-error">
        Preview failed: {error.message}
      </p>
    );
  if (!preview) return null;
  return (
    <div className="space-y-4">
      <div className="rounded-md border border-border bg-muted/20 px-3 py-2 text-xs">
        <p className="font-medium text-foreground">
          {releaseName} · {chartName}@{version}
        </p>
        <p className="text-muted-foreground">Namespace {namespace}</p>
      </div>
      <section aria-label="Installation checks" className="space-y-2">
        {preview.checks.map((check) => (
          <div
            key={check.code}
            className={cn(
              "rounded-md border px-3 py-2 text-xs",
              check.status === "blocking"
                ? "border-status-error/30 bg-status-error/5"
                : check.status === "advisory" || check.status === "approval"
                  ? "border-status-warning/30 bg-status-warning/5"
                  : "border-status-success/30 bg-status-success/5",
            )}
          >
            <p className="font-semibold text-foreground">{check.title}</p>
            <p className="mt-0.5 text-muted-foreground">{check.description}</p>
          </div>
        ))}
      </section>
      <dl className="grid gap-2 text-11 text-muted-foreground sm:grid-cols-2">
        <div>
          <dt>Artifact digest</dt>
          <Tooltip content={preview.artifact_digest}>
            <dd className="truncate font-mono">{preview.artifact_digest}</dd>
          </Tooltip>
        </div>
        <div>
          <dt>Values digest</dt>
          <Tooltip content={preview.values_digest}>
            <dd className="truncate font-mono">{preview.values_digest}</dd>
          </Tooltip>
        </div>
      </dl>
    </div>
  );
}
