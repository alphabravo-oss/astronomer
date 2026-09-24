import { ArrayRows, PathField } from "./guided-array-fields";
import type { GuidedSectionProps } from "./guided-resource-fields";
import { AdvancedAffinityFields } from "./guided-affinity-fields";

export function SchedulingFields({ form }: GuidedSectionProps) {
  if (!form.podPath) return null;
  return (
    <>
      <ArrayRows
        form={form}
        path={[...form.podPath, "tolerations"]}
        label="Tolerations"
        create={() => ({ key: "", operator: "Exists" })}
      >
        {(path) => (
          <>
            <PathField
              form={form}
              path={[...path, "key"]}
              label="Toleration key"
            />
            <PathField
              form={form}
              path={[...path, "operator"]}
              label="Toleration operator"
              options={["Exists", "Equal"]}
            />
            <PathField
              form={form}
              path={[...path, "value"]}
              label="Toleration value"
            />
            <PathField
              form={form}
              path={[...path, "effect"]}
              label="Effect"
              options={["NoSchedule", "PreferNoSchedule", "NoExecute"]}
            />
            <PathField
              form={form}
              path={[...path, "tolerationSeconds"]}
              type="number"
              label="Toleration seconds"
            />
          </>
        )}
      </ArrayRows>
      <ArrayRows
        form={form}
        path={[
          ...form.podPath,
          "affinity",
          "nodeAffinity",
          "requiredDuringSchedulingIgnoredDuringExecution",
          "nodeSelectorTerms",
        ]}
        label="Required node affinity terms (OR)"
        create={() => ({
          matchExpressions: [{ key: "", operator: "In", values: [] }],
        })}
      >
        {(term) => (
          <ArrayRows
            form={form}
            path={[...term, "matchExpressions"]}
            label="Match expressions (AND)"
            create={() => ({ key: "", operator: "In", values: [] })}
          >
            {(path) => (
              <>
                <PathField
                  form={form}
                  path={[...path, "key"]}
                  label="Node label key"
                />
                <PathField
                  form={form}
                  path={[...path, "operator"]}
                  label="Match operator"
                  options={[
                    "In",
                    "NotIn",
                    "Exists",
                    "DoesNotExist",
                    "Gt",
                    "Lt",
                  ]}
                />
                <PathField
                  form={form}
                  path={[...path, "values"]}
                  label="Match values"
                  type="csv"
                />
              </>
            )}
          </ArrayRows>
        )}
      </ArrayRows>
      <AdvancedAffinityFields form={form} />
      <p className="text-xs text-muted-foreground md:col-span-2">
        Other scheduling fields, including node matchFields, are preserved.
        Server dry-run validates scheduling constraints before saving.
      </p>
    </>
  );
}
