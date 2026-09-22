import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { FormShell } from "@/components/ui/form-shell";
import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Field } from "@/components/form/fields";
import {
  ErrorMessage,
  inputClass,
  textareaClass,
} from "@/components/delivery/shared";
import {
  updateDeliveryTarget,
  type DeliveryTarget,
} from "@/lib/api/delivery-targets";
import type { DriftPolicy } from "@/lib/api/delivery-bundles";
import { placementFormDefaults } from "@/components/delivery/target-form";
import { TargetOverridesEditor } from "@/components/delivery/target-overrides-editor";
import { queryKeys } from "@/lib/query-keys";
import { toastSuccess } from "@/lib/toast";
import { buildTargetUpdatePayload } from "./-target-edit-form";
import { PlacementSelectorFieldset, ReconciliationFieldset } from "./-target-edit-fields";

export function TargetEditDialog({
  projectId,
  target,
  etag,
  onUpdated,
  onClose,
}: {
  projectId: string;
  target: DeliveryTarget;
  etag: string | number;
  onUpdated: () => void;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const defaults = placementFormDefaults(target.placement);
  const [allClusters, setAllClusters] = useState(defaults.allClusters);
  const [drift, setDrift] = useState<DriftPolicy>(
    target.reconciliationPolicy.drift,
  );
  const [formError, setFormError] = useState<Error | null>(null);
  const mutation = useMutation({
    mutationFn: (body: Parameters<typeof updateDeliveryTarget>[1]) =>
      updateDeliveryTarget(target.id, body, etag, crypto.randomUUID()),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.target(projectId, target.id),
      });
      client.invalidateQueries({
        queryKey: queryKeys.delivery.targetsAll(projectId),
      });
      onUpdated();
      toastSuccess("Target configuration updated");
      onClose();
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(null);
    try {
      const form = new FormData(event.currentTarget);
      mutation.mutate(
        buildTargetUpdatePayload(form, allClusters, drift, projectId),
      );
    } catch (error) {
      setFormError(
        error instanceof Error
          ? error
          : new Error("Target configuration is invalid."),
      );
    }
  };
  return (
    <ModalShell
      title={`Edit ${target.name}`}
      size="xl"
      onClose={onClose}
      subtitle="Saving changes increments the target generation. Run a new authoritative preview before launching."
    >
      <FormShell className="space-y-5" onSubmit={submit}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Description">
            <Input
              name="description"
              maxLength={4096}
              defaultValue={target.description ?? ""}
              className={inputClass}
            />
          </Field>
          <Field label="Immutable bundle version ID">
            <Input
              name="bundle_version_id"
              required
              defaultValue={target.bundleVersionId}
              className={inputClass}
            />
          </Field>
        </div>
        <PlacementSelectorFieldset
          allClusters={allClusters}
          setAllClusters={setAllClusters}
          defaults={defaults}
        />
        <ReconciliationFieldset
          target={target}
          drift={drift}
          setDrift={setDrift}
        />
        <TargetOverridesEditor
          value={{
            helm_values: target.overrides.helmValues,
            patches: target.overrides.patches,
          }}
          digest={target.overrideDigest}
        />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Maintenance policy (JSON object)">
            <Textarea
              name="maintenance"
              defaultValue={JSON.stringify(
                target.maintenanceWindowPolicy,
                null,
                2,
              )}
              className={textareaClass}
              spellCheck={false}
            />
          </Field>
          <label className="flex items-center gap-2 pt-8 text-sm">
            <Input
              name="approval_required"
              type="checkbox"
              defaultChecked={target.rolloutPolicy.approvalRequired}
            />
            Require human rollout approval
          </label>
        </div>
        {(formError || mutation.isError) && (
          <ErrorMessage error={formError ?? mutation.error} />
        )}
        <div className="flex justify-end gap-2">
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            disabled={mutation.isPending}
            loading={mutation.isPending}
            loadingLabel="Saving…"
          >
            Save and require new preview
          </ActionButton>
        </div>
      </FormShell>
    </ModalShell>
  );
}
