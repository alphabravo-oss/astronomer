import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/backup/destinations/new — add an S3 destination for
 * Astronomer's own management-plane backup (see settings/backup/index.tsx
 * for the read side and the "edit destination" modal).
 */
import { useId, useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ShieldCheck } from "lucide-react";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Switch } from "@/components/ui/switch";
import { useCreateManagementBackupDestination } from "@/components/settings/hooks";
import type { ManagementBackupDestinationWrite } from "@/lib/api/settings-backup-drill";

const DEFAULT_FORM: ManagementBackupDestinationWrite = {
  name: "",
  bucket: "",
  prefix: "astronomer-pg",
  region: "us-east-1",
  endpoint_url: "",
  access_key: "",
  secret_key: "",
  schedule: "0 3 * * *",
  enabled: true,
  keep_daily: 30,
  keep_weekly: 12,
  keep_monthly: 6,
};

function LabeledField({
  label,
  helper,
  children,
}: {
  label: string;
  helper?: string;
  children: (id: string) => React.ReactNode;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground" htmlFor={id}>
        {label}
      </label>
      {children(id)}
      {helper && <p className="text-xs text-muted-foreground">{helper}</p>}
    </div>
  );
}

function NewDestinationForm() {
  const navigate = useNavigate();
  const create = useCreateManagementBackupDestination();
  const [form, setForm] =
    useState<ManagementBackupDestinationWrite>(DEFAULT_FORM);

  const set = <K extends keyof ManagementBackupDestinationWrite>(
    key: K,
    value: ManagementBackupDestinationWrite[K],
  ) => setForm((current) => ({ ...current, [key]: value }));

  const handleCreate = async () => {
    try {
      await create.mutateAsync(form);
      void navigate({ to: "/dashboard/settings/backup" });
    } catch {
      // mutation toasts on error
    }
  };

  return (
    <div className="space-y-6">
      <Card radius="xl" padding="lg" className="space-y-4">
        <LabeledField label="Name">
          {(id) => (
            <Input
              id={id}
              value={form.name}
              onChange={(e) => set("name", e.target.value)}
              placeholder="primary"
              data-initial-focus
            />
          )}
        </LabeledField>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <LabeledField label="Bucket">
            {(id) => (
              <Input
                id={id}
                value={form.bucket}
                onChange={(e) => set("bucket", e.target.value)}
                placeholder="astronomer-backups"
              />
            )}
          </LabeledField>
          <LabeledField
            label="Prefix"
            helper="Object key prefix inside the bucket"
          >
            {(id) => (
              <Input
                id={id}
                value={form.prefix}
                onChange={(e) => set("prefix", e.target.value)}
              />
            )}
          </LabeledField>
          <LabeledField label="Region">
            {(id) => (
              <Input
                id={id}
                value={form.region}
                onChange={(e) => set("region", e.target.value)}
              />
            )}
          </LabeledField>
          <LabeledField
            label="Endpoint"
            helper="Leave blank for AWS. Set for MinIO or other S3-compatible stores."
          >
            {(id) => (
              <Input
                id={id}
                value={form.endpoint_url}
                onChange={(e) => set("endpoint_url", e.target.value)}
                placeholder="https://minio.example.com"
              />
            )}
          </LabeledField>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <LabeledField label="Access key">
            {(id) => (
              <Input
                id={id}
                type="password"
                value={form.access_key}
                onChange={(e) => set("access_key", e.target.value)}
              />
            )}
          </LabeledField>
          <LabeledField label="Secret key">
            {(id) => (
              <Input
                id={id}
                type="password"
                value={form.secret_key}
                onChange={(e) => set("secret_key", e.target.value)}
              />
            )}
          </LabeledField>
        </div>
        <LabeledField label="Cron schedule" helper="UTC. Default is 03:00 every day.">
          {(id) => (
            <Input
              id={id}
              value={form.schedule}
              onChange={(e) => set("schedule", e.target.value)}
            />
          )}
        </LabeledField>
        <div className="grid grid-cols-3 gap-4">
          <LabeledField label="Keep daily">
            {(id) => (
              <Input
                id={id}
                type="number"
                min={1}
                max={365}
                value={form.keep_daily}
                onChange={(e) => set("keep_daily", Number(e.target.value))}
              />
            )}
          </LabeledField>
          <LabeledField label="Keep weekly">
            {(id) => (
              <Input
                id={id}
                type="number"
                min={1}
                max={52}
                value={form.keep_weekly}
                onChange={(e) => set("keep_weekly", Number(e.target.value))}
              />
            )}
          </LabeledField>
          <LabeledField label="Keep monthly">
            {(id) => (
              <Input
                id={id}
                type="number"
                min={1}
                max={36}
                value={form.keep_monthly}
                onChange={(e) => set("keep_monthly", Number(e.target.value))}
              />
            )}
          </LabeledField>
        </div>
        <div className="flex items-center gap-2">
          <Switch
            id="destination-enabled"
            checked={form.enabled}
            onCheckedChange={(checked) => set("enabled", checked)}
          />
          <label
            htmlFor="destination-enabled"
            className="text-sm text-foreground"
          >
            Enabled — write the CronJob as soon as this is saved
          </label>
        </div>
      </Card>

      <div className="flex items-center justify-end gap-2">
        <ActionButton
          onClick={() =>
            void navigate({ to: "/dashboard/settings/backup" })
          }
        >
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={() => void handleCreate()}
          disabled={
            create.isPending ||
            !form.name ||
            !form.bucket ||
            !form.access_key ||
            !form.secret_key
          }
          loading={create.isPending}
        >
          Add destination
        </ActionButton>
      </div>
    </div>
  );
}

function NewDestinationPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings/backup"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Astronomer backup
        </RouterLink>
        <PageHeader
          eyebrow="Settings · Backup · New"
          title={
            <span className="flex items-center gap-2">
              <ShieldCheck className="h-5 w-5 text-muted-foreground" />
              Add S3 destination
            </span>
          }
          description="Credentials are stored encrypted. The dump CronJob starts as soon as you save."
        />
        <NewDestinationForm />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute(
  "/dashboard/settings/backup/destinations/new/",
)({
  component: NewDestinationPage,
});
