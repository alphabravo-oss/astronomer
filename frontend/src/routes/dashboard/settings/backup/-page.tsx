/**
 * /dashboard/settings/backup — Astronomer's own management-plane backup.
 *
 * This is the pg_dump CronJob that copies Astronomer's Postgres (clusters,
 * projects, RBAC, audit, …) to object storage. Workload/app snapshots are
 * Velero, live on each cluster, and only appear after Velero is installed
 * there. Restore of this dump is an operator procedure (not a one-click UI).
 */
import { Link as RouterLink } from "@tanstack/react-router";
import { ArrowLeft, KeyRound, ShieldAlert } from "lucide-react";
import { cn } from "@/lib/utils";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { PageHeader, PageShell } from "@/components/ui/page";
import { QueryStates } from "@/components/ui/query-states";
import { useManagementBackupStatus } from "@/components/settings/hooks";
import type { ManagementBackupStatusView } from "@/lib/api/settings";
import { DestinationsSection } from "./-destinations-section";
import { HistoryTable, LatestDrillCard } from "./-drill";

function EncryptionCard({ data }: { data: ManagementBackupStatusView }) {
  if (!(data.destinations ?? []).length) return null;
  const wrapped = data.encryptionKeyBackup?.wrappingConfigured;
  return (
    <div
      className={cn(
        "rounded-xl border p-6 space-y-2",
        wrapped
          ? "border-border bg-card"
          : "border-status-warning/30 bg-status-warning/5",
      )}
    >
      <div className="flex items-center gap-2">
        {wrapped ? (
          <KeyRound className="h-4 w-4 text-muted-foreground" />
        ) : (
          <ShieldAlert className="h-4 w-4 text-status-warning" />
        )}
        <h2 className="text-sm font-medium text-foreground">
          Encryption key backup
        </h2>
      </div>
      {wrapped ? (
        <p className="text-xs text-muted-foreground">
          The dump and platform key bundle use authenticated client-side
          encryption and a source-bound manifest. A restore can reject tampering
          before it touches PostgreSQL.
        </p>
      ) : (
        <p className="text-xs text-status-warning">
          Authenticated backup encryption is not configured. Restoring onto a
          new cluster would leave encrypted columns undecryptable. Set
          managementBackup.encryption.wrappingSecretRef and sourceIdentity in
          Helm values.
        </p>
      )}
    </div>
  );
}

export function AstronomerBackupPage() {
  const backupQuery = useManagementBackupStatus();

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
          eyebrow="Settings · Backup"
          title="Astronomer backup"
          description="Nightly dump of Astronomer's own database to one or more S3 buckets. Workload snapshots live on each cluster after Velero is installed there."
        />
        <QueryStates
          query={backupQuery}
          loadingTitle="Loading backup configuration"
          permission="settings:read"
          errorTitle="Failed to load backup configuration"
        >
          {(data) => (
            <>
              <DestinationsSection data={data} />
              <EncryptionCard data={data} />
            </>
          )}
        </QueryStates>
        <LatestDrillCard />
        <HistoryTable />
      </PageShell>
    </SettingsAuthGate>
  );
}
