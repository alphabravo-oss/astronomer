import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Plus, ShieldCheck } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable } from "@/components/ui/data-table";
import { OperationMutationTimeline } from "@/components/ui/operation-mutation-timeline";
import { EmptyState } from "@/components/ui/empty-state";
import {
  useDeleteManagementBackupDestination,
  useRunManagementBackupDestination,
} from "@/components/settings/hooks";
import type {
  ManagementBackupDestinationView,
  ManagementBackupStatusView,
} from "@/lib/api/settings";
import { destinationColumns } from "./-destination-columns";
import { DestinationModal } from "./-destination-modal";

export function DestinationsSection({
  data,
}: {
  data: ManagementBackupStatusView;
}) {
  const navigate = useNavigate();
  const [editor, setEditor] = useState<ManagementBackupDestinationView | null>(
    null,
  );
  const [remove, setRemove] = useState<ManagementBackupDestinationView | null>(
    null,
  );
  const del = useDeleteManagementBackupDestination();
  const run = useRunManagementBackupDestination();
  const rows = data.destinations ?? [];

  return (
    <div className="space-y-3">
      <div className="flex flex-col items-start justify-between gap-3 sm:flex-row sm:items-center">
        <div>
          <h2 className="text-base font-semibold text-foreground">
            S3 destinations
          </h2>
          <p className="text-xs text-muted-foreground">
            Each destination gets its own nightly dump CronJob. Add a second
            bucket for DR or a different schedule.
          </p>
        </div>
        <ActionButton
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() =>
            void navigate({
              to: "/dashboard/settings/backup/destinations/new",
            })
          }
        >
          Add destination
        </ActionButton>
      </div>
      {rows.length === 0 ? (
        <EmptyState
          icon={ShieldCheck}
          title="No backup destinations"
          description={
            data.reason ||
            "Add an S3 bucket to start nightly dumps of Astronomer’s database."
          }
          actionLabel="Add destination"
          actionIcon={Plus}
          actionHref="/dashboard/settings/backup/destinations/new"
          className="rounded-xl border border-dashed border-border bg-card p-6"
        />
      ) : (
        <DataTable
          data={rows}
          columns={destinationColumns({
            onRun: (row) => run.mutate(row.id),
            runDisabled: run.isPending,
            onEdit: setEditor,
            onRemove: setRemove,
          })}
          keyExtractor={(row) => row.id}
          emptyState={{
            title: "No destinations",
            description:
              "Resources will appear here when they are available in this scope.",
          }}
        />
      )}
      {editor && (
        <DestinationModal existing={editor} onClose={() => setEditor(null)} />
      )}
      <p className="sr-only" role="status" aria-live="polite">
        {run.isPending
          ? `Backup run ${run.operationState.phase}`
          : del.isPending
            ? "Backup destination removal queued"
            : ""}
      </p>
      <OperationMutationTimeline
        label="Backup run"
        state={run.operationState}
      />
      <ConfirmDialog
        open={!!remove}
        onClose={() => setRemove(null)}
        title="Remove destination"
        description={
          remove
            ? `Stop dumping to ${remove.bucket} and delete the CronJob.`
            : ""
        }
        confirmText="Remove"
        onConfirm={() => {
          if (remove) del.mutate(remove.id);
          setRemove(null);
        }}
      />
    </div>
  );
}
