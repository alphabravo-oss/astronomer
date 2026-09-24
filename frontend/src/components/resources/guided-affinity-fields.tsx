import { ArrayRows, PathField } from "./guided-array-fields";
import { Field, type GuidedFormState } from "./guided-resource-fields";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  manifestValue,
  keyValueText,
  parseKeyValueText,
  type ManifestPath,
} from "./guided-resource-model";

type Props = { form: GuidedFormState; path: ManifestPath };

export function MatchExpressions({
  form,
  path,
  node = false,
}: Props & { node?: boolean }) {
  return (
    <ArrayRows
      form={form}
      path={path}
      label="Match expressions (AND)"
      create={() => ({ key: "", operator: "In", values: [] })}
    >
      {(item) => (
        <>
          <PathField
            form={form}
            path={[...item, "key"]}
            label={node ? "Node label key" : "Label key"}
          />
          <PathField
            form={form}
            path={[...item, "operator"]}
            label="Match operator"
            options={[
              "In",
              "NotIn",
              "Exists",
              "DoesNotExist",
              ...(node ? ["Gt", "Lt"] : []),
            ]}
          />
          <PathField
            form={form}
            path={[...item, "values"]}
            label="Match values"
            type="csv"
          />
        </>
      )}
    </ArrayRows>
  );
}

function LabelSelector({ form, path }: Props) {
  return (
    <>
      <Field
        label="Match labels"
        description="One key=value per line. An empty selector matches every label set."
      >
        <Textarea
          value={keyValueText(
            manifestValue(form.value, [...path, "matchLabels"]),
          )}
          onChange={(event) =>
            form.set(
              [...path, "matchLabels"],
              parseKeyValueText(event.target.value),
            )
          }
        />
      </Field>
      <MatchExpressions form={form} path={[...path, "matchExpressions"]} />
    </>
  );
}

function OptionalSelector({
  form,
  path,
  label,
  absent,
}: Props & { label: string; absent: string }) {
  const present = manifestValue(form.value, path) != null;
  return (
    <fieldset className="space-y-3 rounded-md border border-border p-3 md:col-span-2">
      <legend>{label}</legend>
      <Field label={`${label} mode`}>
        <Select
          value={present ? "selector" : "absent"}
          onChange={(event) =>
            form.set(path, event.target.value === "selector" ? {} : undefined)
          }
        >
          <option value="absent">{absent}</option>
          <option value="selector">Match labels (empty selects all)</option>
        </Select>
      </Field>
      {present && <LabelSelector form={form} path={path} />}
    </fieldset>
  );
}

function PodTerm({ form, path }: Props) {
  return (
    <>
      <PathField
        form={form}
        path={[...path, "topologyKey"]}
        label="Topology key"
      />
      <PathField
        form={form}
        path={[...path, "namespaces"]}
        label="Namespaces"
        type="csv"
      />
      <OptionalSelector
        form={form}
        path={[...path, "labelSelector"]}
        label="Pod selector"
        absent="No pods (selector omitted)"
      />
      <OptionalSelector
        form={form}
        path={[...path, "namespaceSelector"]}
        label="Namespace selector"
        absent="Listed namespaces, or this pod's namespace"
      />
      <p className="text-xs text-muted-foreground md:col-span-2">
        Namespace names and the namespace selector are combined. An empty
        namespace selector includes all namespaces. Other term fields are
        preserved; server dry-run validates cluster-specific rules.
      </p>
    </>
  );
}

export function AdvancedAffinityFields({ form }: { form: GuidedFormState }) {
  if (!form.podPath) return null;
  const affinity = [...form.podPath, "affinity"];
  return (
    <>
      <ArrayRows
        form={form}
        path={[
          ...affinity,
          "nodeAffinity",
          "preferredDuringSchedulingIgnoredDuringExecution",
        ]}
        label="Preferred node affinity terms"
        create={() => ({
          weight: 1,
          preference: {
            matchExpressions: [{ key: "", operator: "In", values: [] }],
          },
        })}
      >
        {(path) => (
          <>
            <PathField
              form={form}
              path={[...path, "weight"]}
              label="Weight (1–100)"
              type="number"
            />
            <MatchExpressions
              form={form}
              path={[...path, "preference", "matchExpressions"]}
              node
            />
          </>
        )}
      </ArrayRows>
      {(["podAffinity", "podAntiAffinity"] as const).map((kind) => (
        <div key={kind} className="space-y-3 md:col-span-2">
          <ArrayRows
            form={form}
            path={[
              ...affinity,
              kind,
              "requiredDuringSchedulingIgnoredDuringExecution",
            ]}
            label={`Required ${kind === "podAffinity" ? "pod affinity" : "pod anti-affinity"} terms`}
            create={() => ({ topologyKey: "kubernetes.io/hostname" })}
          >
            {(path) => <PodTerm form={form} path={path} />}
          </ArrayRows>
          <ArrayRows
            form={form}
            path={[
              ...affinity,
              kind,
              "preferredDuringSchedulingIgnoredDuringExecution",
            ]}
            label={`Preferred ${kind === "podAffinity" ? "pod affinity" : "pod anti-affinity"} terms`}
            create={() => ({
              weight: 1,
              podAffinityTerm: { topologyKey: "kubernetes.io/hostname" },
            })}
          >
            {(path) => (
              <>
                <PathField
                  form={form}
                  path={[...path, "weight"]}
                  label="Weight (1–100)"
                  type="number"
                />
                <PodTerm form={form} path={[...path, "podAffinityTerm"]} />
              </>
            )}
          </ArrayRows>
        </div>
      ))}
    </>
  );
}
