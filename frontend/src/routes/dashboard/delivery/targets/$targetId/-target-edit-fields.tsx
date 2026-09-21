import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Select } from "@/components/ui/select";
import { Field } from "@/components/form/fields";
import { inputClass, textareaClass } from "@/components/delivery/shared";
import type { DriftPolicy } from "@/lib/api/delivery-bundles";
import type { DeliveryTarget } from "@/lib/api/delivery-targets";
import type { placementFormDefaults } from "@/components/delivery/target-form";

export function PlacementSelectorFieldset({
  allClusters,
  setAllClusters,
  defaults,
}: {
  allClusters: boolean;
  setAllClusters: (value: boolean) => void;
  defaults: ReturnType<typeof placementFormDefaults>;
}) {
  return (
    <fieldset className="space-y-4 rounded-md border border-border p-4">
      <legend className="px-1 text-sm font-medium">Placement selector</legend>
      <label className="flex items-center gap-2 text-sm font-medium text-status-warning">
        <Input
          type="checkbox"
          checked={allClusters}
          onChange={(event) => setAllClusters(event.target.checked)}
        />
        Select every eligible cluster in this project
      </label>
      {!allClusters && (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Explicit cluster IDs (comma-separated)">
            <Textarea
              name="cluster_ids"
              defaultValue={defaults.clusterIds}
              className={textareaClass}
            />
          </Field>
          <Field label="Cluster group IDs (comma-separated)">
            <Textarea
              name="group_ids"
              defaultValue={defaults.groupIds}
              className={textareaClass}
            />
          </Field>
          <Field label="Match labels (one key=value per line)">
            <Textarea
              name="labels"
              defaultValue={defaults.labels}
              className={textareaClass}
            />
          </Field>
          <Field label="Expressions (one per line)">
            <Textarea
              name="expressions"
              defaultValue={defaults.expressions}
              className={textareaClass}
            />
          </Field>
        </div>
      )}
      <Field label="Exclude cluster IDs (comma-separated)">
        <Textarea
          name="exclude_ids"
          defaultValue={defaults.excludeIds}
          className={textareaClass}
        />
      </Field>
    </fieldset>
  );
}

export function ReconciliationFieldset({
  target,
  drift,
  setDrift,
}: {
  target: DeliveryTarget;
  drift: DriftPolicy;
  setDrift: (value: DriftPolicy) => void;
}) {
  return (
    <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
      <legend className="px-1 text-sm font-medium">Reconciliation</legend>
      <Field label="Interval">
        <Input
          name="interval"
          required
          defaultValue={target.reconciliationPolicy.interval}
          className={inputClass}
        />
      </Field>
      <Field label="Retry interval">
        <Input
          name="retry_interval"
          required
          defaultValue={target.reconciliationPolicy.retryInterval}
          className={inputClass}
        />
      </Field>
      <Field label="Timeout">
        <Input
          name="timeout"
          required
          defaultValue={target.reconciliationPolicy.timeout}
          className={inputClass}
        />
      </Field>
      <Field label="Drift">
        <Select
          value={drift}
          onChange={(event) => setDrift(event.target.value as DriftPolicy)}
          className={inputClass}
        >
          <option value="repair">Detect and repair</option>
          <option value="detect">Detect only</option>
          <option value="ignore">Ignore</option>
        </Select>
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <Input
          name="prune"
          type="checkbox"
          defaultChecked={target.reconciliationPolicy.prune}
        />
        Prune removed objects
      </label>
      <label className="flex items-center gap-2 text-sm">
        <Input
          name="wait"
          type="checkbox"
          defaultChecked={target.reconciliationPolicy.wait}
        />
        Wait for health
      </label>
    </fieldset>
  );
}
