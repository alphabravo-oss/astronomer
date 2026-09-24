import { describe, expect, it } from "vitest";
import {
  envText,
  parseEnvText,
  updateManifest,
  validateGuidedResource,
} from "../guided-resource-model";
import { replaceVolumeSource } from "../guided-volume-model";

const podPath = ["spec", "template", "spec"];
const containerPath = [...podPath, "containers", 0];
const pod = {
  containers: [
    {
      name: "app",
      image: "nginx",
      ports: [{ containerPort: 80 }, { containerPort: 443 }],
      env: [
        { name: "MODE", value: "prod" },
        {
          name: "PASSWORD",
          valueFrom: { secretKeyRef: { name: "db", key: "password" } },
        },
      ],
      envFrom: [
        { secretRef: { name: "secrets" } },
        { configMapRef: { name: "config" } },
      ],
      volumeMounts: [{ name: "cache", mountPath: "/cache" }],
    },
    { name: "sidecar", image: "helper", env: [{ name: "KEEP", value: "yes" }] },
  ],
  volumes: [
    { name: "cache", emptyDir: {} },
    { name: "other", secret: { secretName: "other" } },
  ],
};
const manifest = {
  kind: "Deployment",
  metadata: { name: "test" },
  spec: { template: { spec: pod } },
};

describe("guided manifest preservation", () => {
  it("requires at least one Service port unless it is an ExternalName", () => {
    const service = {
      kind: "Service",
      metadata: { name: "web" },
      spec: { ports: [] },
    };
    expect(validateGuidedResource(service)).toHaveProperty("servicePort");
    expect(
      validateGuidedResource({
        ...service,
        spec: { ...service.spec, type: "ExternalName" },
      }),
    ).not.toHaveProperty("servicePort");
  });
  it("characterizes a scalar edit without touching sibling arrays", () => {
    const changed = updateManifest(
      manifest,
      [...containerPath, "image"],
      "nginx:next",
    ) as typeof manifest;
    expect(changed.spec.template.spec.containers[1]).toEqual(pod.containers[1]);
    expect(changed.spec.template.spec.volumes).toEqual(pod.volumes);
    expect(changed.spec.template.spec.containers[0].envFrom).toEqual(
      pod.containers[0].envFrom,
    );
    expect(changed.spec.template.spec.containers[0].ports).toEqual(
      pod.containers[0].ports,
    );
    expect(manifest.spec.template.spec.containers[0].image).toBe("nginx");
  });

  it("preserves valueFrom entries when literal environment values change", () => {
    const current = pod.containers[0].env!;
    expect(
      parseEnvText(envText(current).replace("prod", "staging"), current),
    ).toEqual([{ name: "MODE", value: "staging" }, current[1]]);
  });

  it("replaces the volume source without renaming mounts or other volumes", () => {
    const result = replaceVolumeSource(
      manifest,
      podPath,
      0,
      "persistentVolumeClaim",
      { claimName: "data-pvc" },
    ) as typeof manifest;
    expect(result.spec.template.spec.volumes[0]).toEqual({
      name: "cache",
      persistentVolumeClaim: { claimName: "data-pvc" },
    });
    expect(result.spec.template.spec.volumes[1]).toEqual(pod.volumes[1]);
    expect(result.spec.template.spec.containers).toEqual(pod.containers);
  });

  it.each(["http", "http-api", 8080])("accepts probe port %s", (port) => {
    const value = updateManifest(
      manifest,
      [...containerPath, "readinessProbe"],
      { httpGet: { port } },
    );
    expect(validateGuidedResource(value)).not.toHaveProperty("readinessProbe");
  });

  it.each(["Bad_Port", "123", "-http", 0, 65536])(
    "rejects probe port %s",
    (port) => {
      const value = updateManifest(
        manifest,
        [...containerPath, "readinessProbe"],
        { httpGet: { port } },
      );
      expect(validateGuidedResource(value)).toHaveProperty("readinessProbe");
    },
  );
});
