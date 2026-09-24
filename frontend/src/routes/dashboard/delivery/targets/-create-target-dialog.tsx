import { useState, type FormEvent } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Select } from "@/components/ui/select";
import { FormShell } from "@/components/ui/form-shell";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { Field } from "@/components/form/fields";
import {
  BundlePicker,
  BundleVersionPicker,
} from "@/components/delivery/pickers";
import {
  ErrorMessage,
  inputClass,
  textareaClass,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  placementFromForm,
  placementHasSelector,
} from "@/components/delivery/target-form";
import {
  TargetOverridesEditor,
  targetOverridesFromForm,
} from "@/components/delivery/target-overrides-editor";
import {
  createDeliveryTarget,
  type DeliveryTargetRequest,
} from "@/lib/api/delivery-targets";
import type { DriftPolicy } from "@/lib/api/delivery-bundles";
import { queryKeys } from "@/lib/query-keys";
import { toastSuccess } from "@/lib/toast";

export default function CreateTargetDialog({
  projectId,
  onClose,
}: {
  projectId: string;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const { entityHref } = useDeliveryWorkspace();
  const [bundleId, setBundleId] = useState("");
  const [allClusters, setAllClusters] = useState(false);
  const [drift, setDrift] = useState<DriftPolicy>("repair");
  const [formError, setFormError] = useState<Error | null>(null);
  const mutation = useMutation({
    mutationFn: (body: DeliveryTargetRequest) =>
      createDeliveryTarget(body, crypto.randomUUID()),
    onSuccess: ({ data }) => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.targetsAll(projectId),
      });
      toastSuccess("Delivery target created");
      onClose();
      void navigate({ to: entityHref("targets", data.id) });
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(null);
    try {
      const form = new FormData(event.currentTarget);
      const placement = placementFromForm(form, allClusters);
      if (!placementHasSelector(placement))
        throw new Error(
          "Select at least one explicit cluster, group, label, or expression. Empty placement selects nothing.",
        );
      const maintenanceText = String(form.get("maintenance") ?? "").trim();
      const maintenance = maintenanceText
        ? (JSON.parse(maintenanceText) as Record<string, unknown>)
        : {};
      mutation.mutate({
        project_id: projectId,
        name: String(form.get("name")).trim(),
        description: String(form.get("description") ?? "").trim() || undefined,
        bundle_version_id: String(form.get("bundle_version_id")),
        placement,
        rollout_policy: {
          approval_required: form.get("approval_required") === "on",
        },
        reconciliation_policy: {
          interval: String(form.get("interval")),
          retry_interval: String(form.get("retry_interval")),
          timeout: String(form.get("timeout")),
          prune: form.get("prune") === "on",
          wait: form.get("wait") === "on",
          drift,
        },
        maintenance_window_policy: maintenance,
        overrides: targetOverridesFromForm(form),
        suspended: form.get("suspended") === "on",
      });
    } catch (error) {
      setFormError(
        error instanceof Error ? error : new Error("Target policy is invalid."),
      );
    }
  };
  return (
    <ModalShell
      title="Create delivery target"
      size="xl"
      onClose={onClose}
      subtitle="Placement is evaluated only by the management plane. Preview and launch remain separate operations."
    >
      <FormShell className="space-y-5" onSubmit={submit}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name">
            <Input
              name="name"
              required
              maxLength={128}
              className={inputClass}
            />
          </Field>
          <Field label="Description">
            <Input name="description" maxLength={4096} className={inputClass} />
          </Field>
          <Field label="Bundle" htmlFor="delivery-bundle">
            <BundlePicker
              projectId={projectId}
              value={bundleId}
              onChange={setBundleId}
            />
          </Field>
          <Field
            label="Ready, verified bundle version"
            htmlFor="delivery-bundle-version"
          >
            <BundleVersionPicker projectId={projectId} bundleId={bundleId} />
          </Field>
        </div>
        <fieldset className="space-y-4 rounded-md border border-border p-4">
          <legend className="px-1 text-sm font-medium">
            Placement selector
          </legend>
          <label className="flex items-center gap-2 text-sm font-medium text-status-warning">
            <Input
              type="checkbox"
              checked={allClusters}
              onChange={(e) => setAllClusters(e.target.checked)}
            />{" "}
            Select every eligible cluster in this project
          </label>
          {!allClusters && (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Explicit cluster IDs (comma-separated)">
                <Textarea name="cluster_ids" className={textareaClass} />
              </Field>
              <Field label="Cluster group IDs (comma-separated)">
                <Textarea name="group_ids" className={textareaClass} />
              </Field>
              <Field label="Match labels (one key=value per line)">
                <Textarea name="labels" className={textareaClass} />
              </Field>
              <Field label="Expressions (one: key Operator value1,value2)">
                <Textarea
                  name="expressions"
                  className={textareaClass}
                  placeholder="environment In production,staging&#10;gpu DoesNotExist"
                />
              </Field>
            </div>
          )}
          <Field label="Exclude cluster IDs (comma-separated)">
            <Textarea name="exclude_ids" className={textareaClass} />
          </Field>
        </fieldset>
        <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
          <legend className="px-1 text-sm font-medium">Reconciliation</legend>
          <Field label="Interval">
            <Input
              name="interval"
              required
              defaultValue="10m"
              className={inputClass}
            />
          </Field>
          <Field label="Retry interval">
            <Input
              name="retry_interval"
              required
              defaultValue="1m"
              className={inputClass}
            />
          </Field>
          <Field label="Timeout">
            <Input
              name="timeout"
              required
              defaultValue="10m"
              className={inputClass}
            />
          </Field>
          <Field label="Drift">
            <Select
              value={drift}
              onChange={(e) => setDrift(e.target.value as DriftPolicy)}
              className={inputClass}
            >
              <option value="repair">Detect and repair</option>
              <option value="detect">Detect only</option>
              <option value="ignore">Ignore</option>
            </Select>
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <Input name="prune" type="checkbox" defaultChecked /> Prune removed
            objects
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Input name="wait" type="checkbox" defaultChecked /> Wait for health
          </label>
        </fieldset>
        <TargetOverridesEditor />
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Maintenance policy (JSON object)">
            <Textarea
              name="maintenance"
              className={textareaClass}
              placeholder="{}"
            />
          </Field>
          <div className="space-y-3 pt-6">
            <label className="flex items-center gap-2 text-sm">
              <Input name="approval_required" type="checkbox" /> Require human
              rollout approval
            </label>
            <label className="flex items-center gap-2 text-sm">
              <Input name="suspended" type="checkbox" /> Create suspended
            </label>
          </div>
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
            loadingLabel="Creating…"
          >
            Create target
          </ActionButton>
        </div>
      </FormShell>
    </ModalShell>
  );
}
