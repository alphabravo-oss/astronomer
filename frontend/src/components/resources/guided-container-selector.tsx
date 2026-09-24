import { ActionButton } from "@/components/ui/action-button";
import { Select } from "@/components/ui/select";
import { Field, type GuidedSectionProps } from "./guided-resource-fields";
import { manifestArray } from "./guided-array-fields";
import { manifestValue } from "./guided-resource-model";

export function ContainerSelector({
  form,
  selected,
  onSelect,
}: GuidedSectionProps & {
  selected: string;
  onSelect: (value: string) => void;
}) {
  if (!form.podPath) return null;
  const pod = form.podPath;
  const regular = manifestArray(
    manifestValue(form.value, [...pod, "containers"]),
  );
  const init = manifestArray(
    manifestValue(form.value, [...pod, "initContainers"]),
  );
  const [group, indexText] = selected.split(":");
  const items = group === "initContainers" ? init : regular;
  const add = (group: string) => {
    const items = manifestArray(manifestValue(form.value, [...pod, group]));
    const names = new Set([...regular, ...init].map((item) => item.name));
    let n = 1;
    while (names.has(`container-${n}`)) n++;
    form.set(
      [...pod, group],
      [...items, { name: `container-${n}`, image: "" }],
    );
    onSelect(`${group}:${items.length}`);
  };
  return (
    <section className="flex flex-wrap items-end gap-3 rounded-lg border border-border p-4">
      <Field label="Edit container">
        <Select
          value={selected}
          onChange={(event) => onSelect(event.target.value)}
        >
          {[
            ["containers", regular],
            ["initContainers", init],
          ].map(([group, rows]) =>
            (rows as Record<string, unknown>[]).map((item, index) => (
              <option key={`${group}:${index}`} value={`${group}:${index}`}>
                {group === "initContainers" ? "Init: " : ""}
                {String(item.name || `Container ${index + 1}`)}
              </option>
            )),
          )}
        </Select>
      </Field>
      <ActionButton type="button" size="sm" onClick={() => add("containers")}>
        Add container
      </ActionButton>
      <ActionButton
        type="button"
        size="sm"
        onClick={() => add("initContainers")}
      >
        Add init container
      </ActionButton>
      <ActionButton
        type="button"
        size="sm"
        disabled={group === "containers" && regular.length <= 1}
        disabledReason="A workload needs at least one container."
        onClick={() => {
          form.set(
            [...pod, group],
            items.filter((_, index) => index !== Number(indexText)),
          );
          onSelect("containers:0");
        }}
      >
        Remove selected container
      </ActionButton>
    </section>
  );
}
