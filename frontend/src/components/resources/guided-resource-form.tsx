import { useEffect, useMemo, useState } from "react";
import { ContainerSelector } from "./guided-container-selector";
import { IngressRulesSection, RoleRulesSection } from "./guided-rule-fields";
import { manifestArray } from "./guided-array-fields";

import {
  GatewaySection,
  RoleBindingSection,
  SecretSection,
} from "@/components/resources/guided-resource-access-sections";
import { AdvancedFieldsSection } from "@/components/resources/guided-resource-advanced-section";
import {
  IdentitySection,
  PrimaryWorkloadSection,
  ServiceSection,
} from "@/components/resources/guided-resource-basic-sections";
import type { GuidedFormState } from "@/components/resources/guided-resource-fields";
import {
  containerPath,
  manifestValue,
  validateGuidedContainer,
  descriptionForPath,
  podSpecPath,
  stringValue,
  updateManifest,
  validateGuidedResource,
  type KubernetesManifest,
  type ManifestPath,
  type SchemaMap,
} from "@/components/resources/guided-resource-model";
import {
  AutoscalingSection,
  DisruptionBudgetSection,
  NetworkPolicySection,
  StorageClaimSection,
} from "@/components/resources/guided-resource-policy-sections";

export {
  descriptionForPath,
  updateManifest,
  validateGuidedResource,
} from "@/components/resources/guided-resource-model";
export type { KubernetesManifest } from "@/components/resources/guided-resource-model";

interface GuidedResourceFormProps {
  value: KubernetesManifest;
  onChange: (value: KubernetesManifest) => void;
  schema: SchemaMap;
  definitions: Record<string, SchemaMap>;
  onValidationChange?: (valid: boolean) => void;
  identityReadOnly?: boolean;
}

export function GuidedResourceForm({
  value,
  onChange,
  schema,
  definitions,
  onValidationChange,
  identityReadOnly = false,
}: GuidedResourceFormProps) {
  const kind = stringValue(value, ["kind"]);
  const errors = useMemo(() => validateGuidedResource(value), [value]);
  const [selected, setSelected] = useState("containers:0");
  const podPath = podSpecPath(kind);
  const [group, index] = selected.split(":");
  const selectedPath =
    podPath &&
    manifestArray(manifestValue(value, [...podPath, group]))[Number(index)]
      ? [...podPath, group, Number(index)]
      : containerPath(kind);
  const selection = selectedPath
    ? `${selectedPath.at(-2)}:${selectedPath.at(-1)}`
    : "containers:0";

  useEffect(() => {
    onValidationChange?.(Object.keys(errors).length === 0);
  }, [errors, onValidationChange]);

  const set = (path: ManifestPath, next: unknown) =>
    onChange(updateManifest(value, path, next));
  const form: GuidedFormState = {
    value,
    kind,
    podPath,
    containerPath: selectedPath,
    errors: {
      ...errors,
      ...Object.fromEntries(
        [
          "image",
          "containerPort",
          "readinessProbe",
          "livenessProbe",
          "startupProbe",
        ].map((key) => [key, ""]),
      ),
      ...(selectedPath ? validateGuidedContainer(value, selectedPath) : {}),
    },
    identityReadOnly,
    onChange,
    doc: (path, fallback) =>
      descriptionForPath(schema, definitions, path) ?? fallback,
    set,
    setNumber: (path, next) =>
      set(path, next === "" ? undefined : Number(next)),
  };

  return (
    <div className="space-y-5 overflow-y-auto p-5">
      <IdentitySection form={form} />
      <ContainerSelector
        form={form}
        selected={selection}
        onSelect={setSelected}
      />
      <PrimaryWorkloadSection form={form} />
      <ServiceSection form={form} />
      <IngressRulesSection form={form} />
      <AutoscalingSection form={form} />
      <StorageClaimSection form={form} />
      <DisruptionBudgetSection form={form} />
      <NetworkPolicySection form={form} />
      <SecretSection form={form} />
      <RoleRulesSection form={form} />
      <RoleBindingSection form={form} />
      <GatewaySection form={form} />
      <AdvancedFieldsSection form={form} />
      {Object.keys(errors).length > 0 && (
        <p role="alert" className="text-sm text-status-error">
          Fields need attention:{" "}
          {Object.entries(errors)
            .map(([key, message]) => `${key}: ${message}`)
            .join("; ")}
        </p>
      )}
    </div>
  );
}
