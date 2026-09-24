import { Field, type GuidedSectionProps } from "./guided-resource-fields";
import { ArrayRows, PathField } from "./guided-array-fields";
import { manifestValue, stringValue } from "./guided-resource-model";
import { Select } from "@/components/ui/select";

export function EnvironmentFields({ form }: GuidedSectionProps) {
  if (!form.containerPath) return null;
  return (
    <>
      <ArrayRows
        form={form}
        path={[...form.containerPath, "env"]}
        label="Environment variables"
        create={() => ({ name: "", value: "" })}
      >
        {(path) => {
          const entry = manifestValue(form.value, path) as Record<
            string,
            unknown
          >;
          const ref = entry.valueFrom as Record<string, unknown> | undefined;
          const type = ref ? Object.keys(ref)[0] : "literal";
          return (
            <>
              <PathField
                form={form}
                path={[...path, "name"]}
                label="Variable name"
              />
              <Field label="Variable source">
                <Select
                  value={type}
                  onChange={(event) => {
                    const { value: _value, valueFrom: _ref, ...rest } = entry;
                    const source = event.target.value;
                    form.set(
                      path,
                      source === "literal"
                        ? { ...rest, value: "" }
                        : { ...rest, valueFrom: { [source]: {} } },
                    );
                  }}
                >
                  <option value="literal">Literal</option>
                  <option value="secretKeyRef">Secret key</option>
                  <option value="configMapKeyRef">ConfigMap key</option>
                  <option value="fieldRef">Pod field</option>
                  <option value="resourceFieldRef">Container resource</option>
                  {type &&
                    ![
                      "literal",
                      "secretKeyRef",
                      "configMapKeyRef",
                      "fieldRef",
                      "resourceFieldRef",
                    ].includes(type) && (
                      <option value={type}>{type} (edit in YAML)</option>
                    )}
                </Select>
              </Field>
              {type === "literal" ? (
                <PathField
                  form={form}
                  path={[...path, "value"]}
                  label="Variable value"
                />
              ) : type === "secretKeyRef" || type === "configMapKeyRef" ? (
                <>
                  <PathField
                    form={form}
                    path={[...path, "valueFrom", type, "name"]}
                    label="Source name"
                  />
                  <PathField
                    form={form}
                    path={[...path, "valueFrom", type, "key"]}
                    label="Source key"
                  />
                </>
              ) : type === "fieldRef" ? (
                <PathField
                  form={form}
                  path={[...path, "valueFrom", type, "fieldPath"]}
                  label="Field path"
                />
              ) : type === "resourceFieldRef" ? (
                <>
                  <PathField
                    form={form}
                    path={[...path, "valueFrom", type, "resource"]}
                    label="Resource field"
                  />
                  <PathField
                    form={form}
                    path={[...path, "valueFrom", type, "divisor"]}
                    label="Divisor"
                  />
                </>
              ) : null}
            </>
          );
        }}
      </ArrayRows>
      <ArrayRows
        form={form}
        path={[...form.containerPath, "envFrom"]}
        label="Environment sources"
        create={() => ({ configMapRef: { name: "" } })}
      >
        {(path) => {
          const type = manifestValue(form.value, [...path, "secretRef"])
            ? "secretRef"
            : "configMapRef";
          return (
            <>
              <Field label="Environment source type">
                <Select
                  value={type}
                  onChange={(event) => {
                    const current = manifestValue(form.value, path) as Record<
                      string,
                      unknown
                    >;
                    const {
                      secretRef: _secret,
                      configMapRef: _config,
                      ...rest
                    } = current;
                    form.set(path, {
                      ...rest,
                      [event.target.value]: {
                        name: stringValue(form.value, [...path, type, "name"]),
                      },
                    });
                  }}
                >
                  <option value="configMapRef">ConfigMap</option>
                  <option value="secretRef">Secret</option>
                </Select>
              </Field>
              <PathField
                form={form}
                path={[...path, type, "name"]}
                label="Environment source name"
              />
              <PathField
                form={form}
                path={[...path, "prefix"]}
                label="Variable prefix"
              />
            </>
          );
        }}
      </ArrayRows>
    </>
  );
}
