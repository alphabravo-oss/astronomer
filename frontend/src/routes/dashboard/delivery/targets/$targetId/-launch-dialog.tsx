import { Input } from "@/components/ui/input";
import { FormShell } from "@/components/ui/form-shell";
import { Select } from "@/components/ui/select";
import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Field } from "@/components/form/fields";
import {
  Detail,
  ErrorMessage,
  inputClass,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  startDeliveryRollout,
  type AmountType,
  type RolloutFailureAction,
  type RolloutStrategyRequest,
  type RolloutStrategyType,
} from "@/lib/api/delivery-rollouts";
import type { DeliveryTarget, PlacementPreview } from "@/lib/api/delivery-targets";
import { queryKeys } from "@/lib/query-keys";
import { toastSuccess } from "@/lib/toast";
import { buildRolloutStrategyPayload } from "./-launch-form";
import { CanaryFields, PartitionedField, SafetyBudgetFields } from "./-launch-fields";

export function LaunchDialog({
  projectId,
  target,
  preview,
  onClose,
}: {
  projectId: string;
  target: DeliveryTarget;
  preview: PlacementPreview;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const { entityHref } = useDeliveryWorkspace();
  const [strategyType, setStrategyType] =
    useState<RolloutStrategyType>("rolling");
  const [maxUnavailableType, setMaxUnavailableType] =
    useState<AmountType>("count");
  const [failureType, setFailureType] = useState<AmountType>("count");
  const [onFailure, setOnFailure] = useState<RolloutFailureAction>("pause");
  const [formError, setFormError] = useState<Error | null>(null);
  const mutation = useMutation({
    mutationFn: (strategy: RolloutStrategyRequest) =>
      startDeliveryRollout(
        target.id,
        {
          project_id: projectId,
          preview_digest: preview.previewDigest,
          confirm_all_clusters: preview.requiresAllConfirmation,
          strategy,
        },
        preview.targetGeneration,
        crypto.randomUUID(),
      ),
    onSuccess: (rollout) => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.rolloutsAll(projectId),
      });
      toastSuccess("Rollout launched");
      onClose();
      void navigate({ to: entityHref("rollouts", rollout.id) });
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(null);
    try {
      const form = new FormData(event.currentTarget);
      const strategy = buildRolloutStrategyPayload(
        form,
        { strategyType, maxUnavailableType, failureType, onFailure },
        preview,
      );
      mutation.mutate(strategy);
    } catch (error) {
      setFormError(
        error instanceof Error
          ? error
          : new Error("Rollout strategy is invalid."),
      );
    }
  };
  return (
    <ModalShell
      title="Launch rollout"
      size="xl"
      onClose={onClose}
      subtitle="This action freezes placement, immutable bundle revision, strategy, budgets, and the previous known-good version."
    >
      <FormShell className="space-y-5" onSubmit={submit}>
        <div className="grid gap-3 rounded-md border border-border bg-muted/20 p-4 sm:grid-cols-3">
          <Detail label="Bundle version" value={preview.bundleVersionId} mono />
          <Detail label="Selected clusters" value={preview.selectedCount} />
          <Detail label="Preview digest" value={preview.previewDigest} mono />
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="Strategy">
            <Select
              value={strategyType}
              onChange={(e) =>
                setStrategyType(e.target.value as RolloutStrategyType)
              }
              className={inputClass}
            >
              <option value="all_at_once">All at once</option>
              <option value="rolling">Rolling</option>
              <option value="canary">Canary</option>
              <option value="partitioned">Partitioned cohorts</option>
            </Select>
          </Field>
          <Field label="Maximum concurrent">
            <Input
              name="max_concurrent"
              required
              type="number"
              min={1}
              defaultValue={10}
              className={inputClass}
            />
          </Field>
          <Field label="Stable shuffle seed (optional)">
            <Input name="shuffle_seed" maxLength={128} className={inputClass} />
          </Field>
        </div>
        <SafetyBudgetFields
          maxUnavailableType={maxUnavailableType}
          setMaxUnavailableType={setMaxUnavailableType}
          failureType={failureType}
          setFailureType={setFailureType}
          onFailure={onFailure}
          setOnFailure={setOnFailure}
        />
        {strategyType === "canary" && <CanaryFields />}
        {strategyType === "partitioned" && <PartitionedField />}
        {target.rolloutPolicy.approvalRequired && (
          <p className="rounded-md border border-status-warning/30 bg-status-warning/10 p-3 text-sm text-status-warning">
            This target requires human approval. The rollout will stop at an
            approval gate bound to its exact digest and expiry.
          </p>
        )}
        {preview.requiresAllConfirmation && (
          <label className="flex items-start gap-2 rounded-md border border-status-error/30 bg-status-error/10 p-3 text-sm">
            <Input name="confirm_all" type="checkbox" className="mt-1" />
            <span>
              <strong>Confirm all eligible project clusters.</strong> This broad
              placement is intentionally protected by enhanced confirmation.
            </span>
          </label>
        )}
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
            loadingLabel="Launching…"
          >
            Launch frozen rollout
          </ActionButton>
        </div>
      </FormShell>
    </ModalShell>
  );
}
