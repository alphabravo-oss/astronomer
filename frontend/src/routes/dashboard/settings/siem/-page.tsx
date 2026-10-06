/**
 * /dashboard/settings/siem — external SIEM forwarders (F-05).
 *
 * List / create / edit / delete syslog / Splunk HEC / NDJSON-HTTPS
 * destinations, a Test button that ships a synthetic event through the real
 * pipeline, and a per-forwarder status drawer (queue depth, dropped /
 * dispatched totals, last error). All endpoints are superuser-gated
 * server-side; SettingsAuthGate mirrors that in the UI.
 */
import { useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, Plus } from "lucide-react";
import { DataTable } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import type { SIEMForwarder } from "@/types";
import {
  useSIEMForwarders,
  useDeleteSIEMForwarder,
  useTestSIEMForwarder,
} from "./-hooks";
import { siemForwarderColumns } from "./-columns";
import { SIEMForwarderModal } from "./-forwarder-modal";
import { SIEMStatusDrawer } from "./-status-drawer";

export { TRANSPORTS, FORMATS } from "./-options";

function SIEMForwardersList() {
  const navigate = useNavigate();
  const { data, isLoading, isError, refetch } = useSIEMForwarders();
  const del = useDeleteSIEMForwarder();
  const test = useTestSIEMForwarder();

  const [editing, setEditing] = useState<SIEMForwarder | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<SIEMForwarder | null>(null);
  const [statusTarget, setStatusTarget] = useState<SIEMForwarder | null>(null);

  const columns = siemForwarderColumns({
    onStatus: setStatusTarget,
    onTest: (row) => test.mutate(row.id),
    testDisabled: test.isPending,
    onEdit: setEditing,
    onDelete: setDeleteTarget,
  });

  return (
    <>
      <div className="flex items-center justify-end">
        <ActionButton
          intent="primary"
          icon={<Plus className="h-4 w-4" />}
          onClick={() => void navigate({ to: "/dashboard/settings/siem/new" })}
        >
          Add Forwarder
        </ActionButton>
      </div>

      <DataTable
        data={data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        searchPlaceholder="Search forwarders..."
        emptyState={{
          title: "No SIEM forwarders configured",
          description: "Create the first item to configure this feature.",
        }}
      />

      {editing && (
        <SIEMForwarderModal
          forwarder={editing}
          onClose={() => setEditing(null)}
        />
      )}

      {statusTarget && (
        <SIEMStatusDrawer
          forwarder={statusTarget}
          onClose={() => setStatusTarget(null)}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return;
          await del.mutateAsync(deleteTarget.id);
          setDeleteTarget(null);
        }}
        title="Delete SIEM forwarder?"
        description={`This removes "${deleteTarget?.name}" and drops any queued events for it. This cannot be undone.`}
        confirmText="Delete"
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={del.isPending}
      />
    </>
  );
}

export default function SIEMForwardersPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </RouterLink>
        <PageHeader
          eyebrow="Settings · SIEM"
          title="SIEM Forwarders"
          description="Stream audit + platform events to external SIEMs over syslog, Splunk HEC, or NDJSON-HTTPS. Use Test to ship a synthetic event through the real pipeline and confirm delivery."
        />
        <SIEMForwardersList />
      </PageShell>
    </SettingsAuthGate>
  );
}
