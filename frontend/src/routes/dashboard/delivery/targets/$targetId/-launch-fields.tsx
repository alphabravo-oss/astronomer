import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Select } from "@/components/ui/select";
import { Field } from "@/components/form/fields";
import { inputClass, textareaClass } from "@/components/delivery/shared";
import type { AmountType, RolloutFailureAction } from "@/lib/api/delivery-rollouts";

export function SafetyBudgetFields({
  maxUnavailableType,
  setMaxUnavailableType,
  failureType,
  setFailureType,
  onFailure,
  setOnFailure,
}: {
  maxUnavailableType: AmountType;
  setMaxUnavailableType: (value: AmountType) => void;
  failureType: AmountType;
  setFailureType: (value: AmountType) => void;
  onFailure: RolloutFailureAction;
  setOnFailure: (value: RolloutFailureAction) => void;
}) {
  return (
    <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
      <legend className="px-1 text-sm font-medium">Safety budgets</legend>
      <Field label="Maximum unavailable">
        <div className="flex gap-2">
          <Select
            value={maxUnavailableType}
            onChange={(e) => setMaxUnavailableType(e.target.value as AmountType)}
            className={inputClass}
          >
            <option value="count">Count</option>
            <option value="percent">Percent</option>
          </Select>
          <Input
            name="max_unavailable"
            required
            type="number"
            min={0}
            defaultValue={1}
            className={inputClass}
          />
        </div>
      </Field>
      <Field label="Failure threshold">
        <div className="flex gap-2">
          <Select
            value={failureType}
            onChange={(e) => setFailureType(e.target.value as AmountType)}
            className={inputClass}
          >
            <option value="count">Count</option>
            <option value="percent">Percent</option>
          </Select>
          <Input
            name="failure_threshold"
            required
            type="number"
            min={1}
            defaultValue={1}
            className={inputClass}
          />
        </div>
      </Field>
      <Field label="On failure">
        <Select
          value={onFailure}
          onChange={(e) => setOnFailure(e.target.value as RolloutFailureAction)}
          className={inputClass}
        >
          <option value="pause">Pause</option>
          <option value="abort">Abort</option>
          <option value="rollback">Roll back known-good version</option>
        </Select>
      </Field>
      <Field label="Minimum ready">
        <Input name="min_ready" required defaultValue="30s" className={inputClass} />
      </Field>
      <Field label="Progress deadline">
        <Input name="deadline" required defaultValue="30m" className={inputClass} />
      </Field>
      <label className="flex items-center gap-2 pt-7 text-sm">
        <Input name="respect_windows" type="checkbox" defaultChecked />{" "}
        Respect maintenance windows
      </label>
    </fieldset>
  );
}

export function CanaryFields() {
  return (
    <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
      <legend className="px-1 text-sm font-medium">Canary cohort</legend>
      <Field label="Explicit canary cluster IDs (optional)">
        <Input name="canary_ids" className={inputClass} />
      </Field>
      <Field label="Canary size if not explicit">
        <div className="flex gap-2">
          <Select name="canary_size_type" className={inputClass}>
            <option value="count">Count</option>
            <option value="percent">Percent</option>
          </Select>
          <Input
            name="canary_size"
            type="number"
            min={1}
            defaultValue={1}
            className={inputClass}
          />
        </div>
      </Field>
      <Field label="Soak">
        <Input name="canary_soak" required defaultValue="5m" className={inputClass} />
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <Input name="approval_after_canary" type="checkbox" defaultChecked />{" "}
        Require approval after canary
      </label>
    </fieldset>
  );
}

export function PartitionedField() {
  return (
    <Field label="Ordered partition definitions (JSON array)">
      <Textarea
        name="partitions"
        required
        className={textareaClass}
        placeholder='[{"name":"staging","selector":{"all_clusters":false,"match_labels":{"environment":"staging"}},"approval_required":true,"soak":"10m"}]'
      />
    </Field>
  );
}
