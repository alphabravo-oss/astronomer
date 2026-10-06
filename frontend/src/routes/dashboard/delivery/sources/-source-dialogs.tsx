import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Input } from "@/components/ui/input";
import { FormShell } from "@/components/ui/form-shell";
import { ModalShell } from "@/components/ui/modal-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ActionButton } from "@/components/ui/action-button";
import { Field } from "@/components/form/fields";
import { ErrorMessage, inputClass } from "@/components/delivery/shared";
import {
  deleteDeliverySource,
  rotateDeliverySourceCredential,
  verifyDeliverySource,
  type DeliverySource,
  type SourceCredentialInput,
} from "@/lib/api/delivery-sources";
import { queryKeys } from "@/lib/query-keys";
import { toastSuccess } from "@/lib/toast";
import { CredentialFields, credentialFromForm } from "./-credential-fields";

export function SourceVerifyDialog({
  projectId,
  source,
  onClose,
}: {
  projectId: string;
  source: DeliverySource;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (values: { revision: string; chart?: string }) =>
      verifyDeliverySource(
        source.id,
        {
          project_id: projectId,
          requested_revision: values.revision,
          chart: values.chart,
        },
        crypto.randomUUID(),
      ),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.sourcesAll(projectId),
      });
      toastSuccess("Source verification queued");
      onClose();
    },
  });
  return (
    <ModalShell title={`Verify ${source.name}`} onClose={onClose}>
      <FormShell
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          const form = new FormData(event.currentTarget);
          mutation.mutate({
            revision: String(form.get("revision")),
            chart: String(form.get("chart") ?? "") || undefined,
          });
        }}
      >
        <Field label="Revision to resolve">
          <Input
            name="revision"
            required
            className={inputClass}
            placeholder="branch, tag, version, or digest"
          />
        </Field>
        {(source.type === "helm_http" || source.type === "helm_oci") && (
          <Field label="Chart">
            <Input name="chart" required className={inputClass} />
          </Field>
        )}
        {mutation.isError && <ErrorMessage error={mutation.error} />}
        <div className="flex justify-end gap-2">
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            disabled={mutation.isPending}
            loading={mutation.isPending}
            loadingLabel="Queuing…"
          >
            Verify immutable revision
          </ActionButton>
        </div>
      </FormShell>
    </ModalShell>
  );
}

export function CredentialDialog({
  projectId,
  source,
  onClose,
}: {
  projectId: string;
  source: DeliverySource;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (credential: SourceCredentialInput) =>
      rotateDeliverySourceCredential(
        source.id,
        { project_id: projectId, auth_mode: source.authMode, credential },
        crypto.randomUUID(),
      ),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.sourcesAll(projectId),
      });
      toastSuccess("Credential rotation started");
      onClose();
    },
  });
  return (
    <ModalShell
      title={`Rotate ${source.name} credentials`}
      onClose={onClose}
      subtitle="Old material is retained downstream until the new credential resolves the approved revision."
    >
      <FormShell
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.mutate(
            credentialFromForm(
              new FormData(event.currentTarget),
              source.authMode,
            ),
          );
        }}
      >
        <CredentialFields mode={source.authMode} />
        {mutation.isError && <ErrorMessage error={mutation.error} />}
        <div className="flex justify-end gap-2">
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            disabled={mutation.isPending}
            loading={mutation.isPending}
          >
            Rotate credential
          </ActionButton>
        </div>
      </FormShell>
    </ModalShell>
  );
}

export function SourceDeleteDialog({
  projectId,
  source,
  onClose,
}: {
  projectId: string;
  source: DeliverySource | null;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: () =>
      source
        ? deleteDeliverySource(projectId, source.id, crypto.randomUUID())
        : Promise.resolve(),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.sourcesAll(projectId),
      });
      toastSuccess("Delivery source deleted");
      onClose();
    },
  });
  return (
    <ConfirmDialog
      open={Boolean(source)}
      onClose={onClose}
      onConfirm={() => mutation.mutate()}
      title="Delete delivery source"
      description={`Delete “${source?.name ?? ""}”? Sources referenced by bundle versions cannot be deleted.`}
      confirmValue={source?.name}
      variant="destructive"
      loading={mutation.isPending}
    />
  );
}
