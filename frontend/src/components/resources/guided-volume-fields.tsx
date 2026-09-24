import { Field, type GuidedSectionProps } from "./guided-resource-fields";
import { ArrayRows, PathField, manifestArray } from "./guided-array-fields";
import { manifestValue, stringValue } from "./guided-resource-model";
import {
  volumeReferences,
  renameVolume,
  replaceVolumeSource,
} from "./guided-volume-model";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";

export function VolumeFields({ form }: GuidedSectionProps) {
  const { podPath: pod, containerPath: container, value } = form;
  if (!pod || !container) return null;
  const volumes = manifestArray(manifestValue(value, [...pod, "volumes"]));
  return (
    <>
      <ArrayRows
        form={form}
        path={[...pod, "volumes"]}
        label="Pod volumes"
        create={() => ({ name: "", emptyDir: {} })}
        canRemove={(volume) =>
          volumeReferences(value, pod, String(volume.name ?? "")).length === 0
        }
      >
        {(path, index) => {
          const volume = manifestValue(value, path) as Record<string, unknown>;
          const source =
            Object.keys(volume).find((key) => key !== "name") || "emptyDir";
          const supported = [
            "emptyDir",
            "persistentVolumeClaim",
            "configMap",
            "secret",
          ].includes(source);
          return (
            <>
              <Field label="Volume name">
                <Input
                  value={stringValue(value, [...path, "name"])}
                  onChange={(event) =>
                    form.onChange(
                      renameVolume(value, pod, index, event.target.value),
                    )
                  }
                />
              </Field>
              <Field label="Volume source">
                <Select
                  value={source}
                  onChange={(event) =>
                    form.onChange(
                      replaceVolumeSource(
                        value,
                        pod,
                        index,
                        event.target.value,
                        {},
                      ),
                    )
                  }
                >
                  <option value="emptyDir">Empty directory</option>
                  <option value="persistentVolumeClaim">PVC</option>
                  <option value="configMap">ConfigMap</option>
                  <option value="secret">Secret</option>
                  {!supported && (
                    <option value={source}>
                      {source} (preserved; edit in YAML)
                    </option>
                  )}
                </Select>
              </Field>
              {source === "persistentVolumeClaim" && (
                <PathField
                  form={form}
                  path={[...path, source, "claimName"]}
                  label="PVC claim"
                />
              )}
              {source === "configMap" && (
                <PathField
                  form={form}
                  path={[...path, source, "name"]}
                  label="ConfigMap name"
                />
              )}
              {source === "secret" && (
                <PathField
                  form={form}
                  path={[...path, source, "secretName"]}
                  label="Secret name"
                />
              )}
              {source === "emptyDir" && (
                <>
                  <PathField
                    form={form}
                    path={[...path, source, "medium"]}
                    label="Medium"
                    options={["Memory"]}
                  />
                  <PathField
                    form={form}
                    path={[...path, source, "sizeLimit"]}
                    label="Size limit"
                  />
                </>
              )}
            </>
          );
        }}
      </ArrayRows>
      <ArrayRows
        form={form}
        path={[...container, "volumeMounts"]}
        label="Volume mounts"
        create={() => ({ name: "", mountPath: "" })}
      >
        {(path) => (
          <>
            <Field label="Mounted volume">
              <Select
                value={stringValue(value, [...path, "name"])}
                onChange={(event) =>
                  form.set([...path, "name"], event.target.value)
                }
              >
                <option value="">Select a pod volume</option>
                {volumes.map((volume, index) => (
                  <option key={index} value={String(volume.name || "")}>
                    {String(volume.name || "Unnamed volume")}
                  </option>
                ))}
                {!volumes.some(
                  (volume) =>
                    volume.name === manifestValue(value, [...path, "name"]),
                ) && (
                  <option value={stringValue(value, [...path, "name"])}>
                    {stringValue(value, [...path, "name"])} (not declared)
                  </option>
                )}
              </Select>
            </Field>
            <PathField
              form={form}
              path={[...path, "mountPath"]}
              label="Volume mount path"
            />
            <PathField
              form={form}
              path={[...path, "subPath"]}
              label="Subpath"
            />
          </>
        )}
      </ArrayRows>
    </>
  );
}
