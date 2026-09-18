import { useState } from "react";
import { Plus, Ship } from "lucide-react";
import { useParams } from "@tanstack/react-router";
import { Link as RouterLink } from "@tanstack/react-router";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader, PageShell } from "@/components/ui/page";
import { useAttachAstronomerLogs, useLoggingAttachStatus } from "@/lib/hooks/logging";
import { useCluster } from "@/lib/hooks/clusters";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { PipelinesTab } from "@/routes/dashboard/logging/-pipelines-tab";
import { CreatePipelineModal } from "@/routes/dashboard/logging/-pipeline-modal";

export function ClusterLoggingPage() {
  const params = useParams({ from: "/dashboard/clusters/$id" });
  const clusterId = params.id;
  const { data: cluster } = useCluster(clusterId);
  const [showPipelineModal, setShowPipelineModal] = useState(false);
  const canCreate = usePermissionDecision("logging", "create", {
    type: "cluster",
    id: clusterId,
  });
  const attachStatus = useLoggingAttachStatus(clusterId);
  const attach = useAttachAstronomerLogs(clusterId);
  const ingestPublic = Boolean(attachStatus.data?.ingestPublic);
  const attached = Boolean(attachStatus.data?.attached);
  const showAttach = ingestPublic && canCreate.allowed;

  return (
    <PageShell>
      <PageHeader
        title="Logging"
        description={`Log pipelines for ${cluster?.displayName || cluster?.name || "this cluster"}`}
        actions={
          <div className="flex items-center gap-2">
            <RouterLink
              to="/dashboard/logging"
              className="inline-flex h-9 items-center justify-center rounded-md border border-border bg-background px-3 text-sm font-medium text-foreground transition-colors hover:bg-accent"
            >
              Destinations
            </RouterLink>
            {showAttach ? (
              <ActionButton
                intent="primary"
                icon={<Ship className="h-4 w-4" />}
                loading={attach.isPending}
                disabled={attached || attach.isPending}
                onClick={() => attach.mutate(false)}
                data-testid="attach-astronomer-logs"
              >
                {attached
                  ? "Astronomer logs attached"
                  : "Ship logs to Astronomer"}
              </ActionButton>
            ) : null}
            <ActionButton
              intent="primary"
              icon={<Plus className="h-4 w-4" />}
              onClick={() => setShowPipelineModal(true)}
            >
              Create Pipeline
            </ActionButton>
          </div>
        }
      />

      {showAttach && !attached ? (
        <p
          className="text-sm text-muted-foreground"
          data-testid="attach-astronomer-disclaimer"
        >
          Astronomer logs is convenience, not compliance. Hosted Loki is a
          fail-closed warehouse; BYO destinations remain first-class.
        </p>
      ) : null}

      <PipelinesTab clusterId={clusterId} />

      {showPipelineModal && (
        <CreatePipelineModal
          clusterId={clusterId}
          onClose={() => setShowPipelineModal(false)}
        />
      )}
    </PageShell>
  );
}
