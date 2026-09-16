import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { CheckCircle2, Download, LifeBuoy, LoaderCircle } from "lucide-react";
import { downloadBlob } from "@/lib/utils";
import { toastError, toastSuccess } from "@/lib/toast";
import { ActionButton } from "@/components/ui/action-button";
import { QueryStates } from "@/components/ui/query-states";
import {
  createSupportBundle,
  downloadSupportBundleArtifact,
  getSupportBundleOperation,
} from "@/lib/api/support-bundles";
import { queryKeys } from "@/lib/query-keys";

export function SupportTab() {
  const [operationId, setOperationId] = useState<string | null>(null);
  const [downloading, setDownloading] = useState(false);

  const createMutation = useMutation({
    mutationFn: () => createSupportBundle(),
    onSuccess: (operation) => setOperationId(operation.id),
    onError: (error) => toastError(error.message),
  });
  const operationQuery = useQuery({
    queryKey: queryKeys.settings.supportBundleOperation(operationId),
    queryFn: ({ signal }) => getSupportBundleOperation(operationId!, signal),
    enabled: operationId !== null,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "succeeded" || status === "failed" ? false : 2000;
    },
  });
  const operation = operationQuery.data;
  const collecting =
    createMutation.isPending ||
    operation?.status === "pending" ||
    operation?.status === "running" ||
    operation?.status === "retrying";

  const handleDownload = async () => {
    if (!operationId) return;
    setDownloading(true);
    try {
      const { blob, filename } = await downloadSupportBundleArtifact(operationId);
      downloadBlob(blob, filename);
      toastSuccess("Support bundle downloaded");
    } catch (err) {
      const message =
        err instanceof Error
          ? err.message
          : "Failed to download support bundle";
      toastError(message);
    } finally {
      setDownloading(false);
    }
  };

  return (
    <div className="max-w-2xl space-y-6">
      <div className="rounded-lg border border-border bg-card p-6 space-y-4">
        <div className="flex items-start gap-3">
          <LifeBuoy className="h-5 w-5 text-muted-foreground shrink-0 mt-0.5" />
          <div className="space-y-1">
            <h3 className="text-sm font-semibold text-foreground">
              Support bundle
            </h3>
            <p className="text-sm text-muted-foreground">
              Collects platform metadata, recent audit entries, Kubernetes
              events, Helm state, and bounded control-plane logs in a durable
              background operation. You can leave this page while it runs.
            </p>
            <p className="text-xs text-muted-foreground">
              Passwords, CA certs, encrypted tokens, credential-shaped values,
              and sensitive pod log lines are redacted. Share the bundle only
              with people authorized to triage this install.
            </p>
          </div>
        </div>
        {operationId && (operationQuery.isLoading || operationQuery.isError) && (
          <QueryStates query={operationQuery} permission="support_bundles:read">
            {() => null}
          </QueryStates>
        )}
        <ActionButton
          intent="primary"
          icon={
            operation?.status === "succeeded" ? (
              <Download className="h-4 w-4" />
            ) : (
              <LoaderCircle className="h-4 w-4" />
            )
          }
          loading={collecting || downloading}
          onClick={
            operation?.status === "succeeded"
              ? handleDownload
              : () => createMutation.mutate()
          }
        >
          {operation?.status === "succeeded"
            ? "Download support bundle"
            : collecting
              ? "Collecting support bundle"
              : "Generate support bundle"}
        </ActionButton>
        {operation && (
          <div
            className="flex items-center gap-2 text-xs text-muted-foreground"
            aria-live="polite"
          >
            {operation.status === "succeeded" ? (
              <CheckCircle2 className="h-4 w-4 text-emerald-500" />
            ) : collecting ? (
              <LoaderCircle className="h-4 w-4 animate-spin" />
            ) : null}
            <span>
              {operation.status === "failed"
                ? `Collection failed: ${operation.error_code || "generation_failed"}`
                : operation.status === "succeeded"
                  ? `Ready · ${(operation.size / 1024 / 1024).toFixed(1)} MiB · retained for 24 hours`
                  : `Status: ${operation.status} · attempt ${Math.max(1, operation.attempt_count)}`}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
