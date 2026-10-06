import { useAppForm } from "@/lib/form";
import { FormShell } from "@/components/ui/form-shell";
import { extractApiErrorMessage } from "@/lib/api/errors";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { OperationMutationTimeline } from "@/components/ui/operation-mutation-timeline";
import {
  useTestManagementBackupDestination,
  useUpdateManagementBackupDestination,
} from "@/components/settings/hooks";
import type { ManagementBackupDestinationView } from "@/lib/api/settings";
import { MANAGEMENT_BACKUP_SECRET_SENTINEL } from "@/lib/api/settings";

type DestForm = {
  name: string;
  bucket: string;
  prefix: string;
  region: string;
  endpoint_url: string;
  access_key: string;
  secret_key: string;
  schedule: string;
  enabled: boolean;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
};

function formFromDest(row: ManagementBackupDestinationView): DestForm {
  return {
    name: row.name,
    bucket: row.bucket,
    prefix: row.prefix || "astronomer-pg",
    region: row.region || "us-east-1",
    endpoint_url: row.endpoint || "",
    access_key: row.hasCredentials ? MANAGEMENT_BACKUP_SECRET_SENTINEL : "",
    secret_key: row.hasCredentials ? MANAGEMENT_BACKUP_SECRET_SENTINEL : "",
    schedule: row.schedule || "0 3 * * *",
    enabled: row.enabled,
    keep_daily: row.keepDaily || 30,
    keep_weekly: row.keepWeekly || 12,
    keep_monthly: row.keepMonthly || 6,
  };
}

export function DestinationModal({
  existing,
  onClose,
}: {
  existing: ManagementBackupDestinationView;
  onClose: () => void;
}) {
  const update = useUpdateManagementBackupDestination();
  const test = useTestManagementBackupDestination();

  const form = useAppForm({
    defaultValues: formFromDest(existing),
    onSubmit: async ({ value }) => {
      const body = {
        name: value.name,
        bucket: value.bucket,
        prefix: value.prefix,
        region: value.region,
        endpoint_url: value.endpoint_url,
        access_key: value.access_key,
        secret_key: value.secret_key,
        schedule: value.schedule,
        enabled: value.enabled,
        keep_daily: value.keep_daily,
        keep_weekly: value.keep_weekly,
        keep_monthly: value.keep_monthly,
      };
      await update.mutateAsync({ id: existing.id, body });
      onClose();
    },
  });

  return (
    <ModalShell
      title="Edit destination"
      subtitle="Credentials are stored encrypted. The dump CronJob starts as soon as you save."
      onClose={onClose}
      size="lg"
      footer={
        <div className="flex justify-end gap-2">
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
          >
            {existing ? "Save" : "Add destination"}
          </ActionButton>
        </div>
      }
    >
      <FormShell
        form={form}
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          void form.handleSubmit();
        }}
      >
        <form.AppForm>
          <form.FormErrorSummary
            serverError={
              update.error ? extractApiErrorMessage(update.error) : null
            }
          />
        </form.AppForm>
        <form.AppField
          name="name"
          validators={{
            onChange: ({ value }) =>
              !value.trim() ? "Name is required" : undefined,
          }}
        >
          {(field) => (
            <field.TextField label="Name" required placeholder="primary" />
          )}
        </form.AppField>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <form.AppField
            name="bucket"
            validators={{
              onChange: ({ value }) =>
                !value.trim() ? "Bucket is required" : undefined,
            }}
          >
            {(field) => (
              <field.TextField
                label="Bucket"
                required
                placeholder="astronomer-backups"
              />
            )}
          </form.AppField>
          <form.AppField name="prefix">
            {(field) => (
              <field.TextField
                label="Prefix"
                helper="Object key prefix inside the bucket"
              />
            )}
          </form.AppField>
          <form.AppField name="region">
            {(field) => <field.TextField label="Region" />}
          </form.AppField>
          <form.AppField name="endpoint_url">
            {(field) => (
              <field.TextField
                label="Endpoint"
                helper="Leave blank for AWS. Set for MinIO or other S3-compatible stores."
                placeholder="https://minio.example.com"
              />
            )}
          </form.AppField>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <form.AppField name="access_key">
            {(field) => (
              <field.SecretField
                label="Access key"
                required
                stored={!!existing?.hasCredentials}
                revealable
              />
            )}
          </form.AppField>
          <form.AppField name="secret_key">
            {(field) => (
              <field.SecretField
                label="Secret key"
                required
                stored={!!existing?.hasCredentials}
                revealable
              />
            )}
          </form.AppField>
        </div>
        <form.AppField name="schedule">
          {(field) => (
            <field.TextField
              label="Cron schedule"
              helper="UTC. Default is 03:00 every day."
            />
          )}
        </form.AppField>
        <div className="grid grid-cols-3 gap-3">
          <form.AppField name="keep_daily">
            {(field) => (
              <field.NumberField label="Keep daily" min={1} max={365} />
            )}
          </form.AppField>
          <form.AppField name="keep_weekly">
            {(field) => (
              <field.NumberField label="Keep weekly" min={1} max={52} />
            )}
          </form.AppField>
          <form.AppField name="keep_monthly">
            {(field) => (
              <field.NumberField label="Keep monthly" min={1} max={36} />
            )}
          </form.AppField>
        </div>
        <form.AppField name="enabled">
          {(field) => (
            <field.SwitchField
              label="Enabled"
              helper="When on, Astronomer writes a CronJob that dumps to this bucket."
            />
          )}
        </form.AppField>
        {existing && (
          <ActionButton
            onClick={() => void test.mutateAsync(existing.id)}
            disabled={test.isPending}
          >
            Test connection
          </ActionButton>
        )}
        <p className="sr-only" role="status" aria-live="polite">
          {test.isPending ? `Connection test ${test.operationState.phase}` : ""}
        </p>
        <OperationMutationTimeline
          label="Connection test"
          state={test.operationState}
        />
      </FormShell>
    </ModalShell>
  );
}
