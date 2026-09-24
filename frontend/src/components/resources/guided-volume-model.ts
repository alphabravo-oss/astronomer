import { manifestArray } from "./guided-array-fields";
import {
  manifestValue,
  stringValue,
  updateManifest,
  type KubernetesManifest,
  type ManifestPath,
} from "./guided-resource-model";

export function volumeReferences(
  value: KubernetesManifest,
  pod: ManifestPath,
  name: string,
) {
  return ["containers", "initContainers"].flatMap((group) =>
    manifestArray(manifestValue(value, [...pod, group])).flatMap(
      (container, index) =>
        ["volumeMounts", "volumeDevices"].flatMap((field) =>
          manifestArray(container[field]).flatMap((mount, mountIndex) =>
            mount.name === name
              ? [[...pod, group, index, field, mountIndex, "name"]]
              : [],
          ),
        ),
    ),
  );
}

export function renameVolume(
  value: KubernetesManifest,
  pod: ManifestPath,
  index: number,
  name: string,
) {
  const old = stringValue(value, [...pod, "volumes", index, "name"]);
  let next = updateManifest(value, [...pod, "volumes", index, "name"], name);
  for (const path of volumeReferences(value, pod, old))
    next = updateManifest(next, path, name);
  return next;
}

export function replaceVolumeSource(
  value: KubernetesManifest,
  pod: ManifestPath,
  index: number,
  source: string,
  config: Record<string, unknown>,
) {
  return updateManifest(value, [...pod, "volumes", index], {
    name: stringValue(value, [...pod, "volumes", index, "name"]),
    [source]: config,
  });
}
