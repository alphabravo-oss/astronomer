import { useEffect, useMemo } from "react";

import {
  GatewaySection,
  RoleBindingSection,
  RoleSection,
  SecretSection,
} from "@/components/resources/guided-resource-access-sections";
import { AdvancedFieldsSection } from "@/components/resources/guided-resource-advanced-section";
import {
  IdentitySection,
  IngressSection,
  PrimaryWorkloadSection,
  ServiceSection,
} from "@/components/resources/guided-resource-basic-sections";
import type { GuidedFormState } from "@/components/resources/guided-resource-fields";
import {
  containerPath,
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

  useEffect(() => {
    onValidationChange?.(Object.keys(errors).length === 0);
  }, [errors, onValidationChange]);

  const set = (path: ManifestPath, next: unknown) =>
    onChange(updateManifest(value, path, next));
  const form: GuidedFormState = {
    value,
    kind,
    podPath: podSpecPath(kind),
    containerPath: containerPath(kind),
    errors,
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
      <PrimaryWorkloadSection form={form} />
      <ServiceSection form={form} />
      <IngressSection form={form} />
      <AutoscalingSection form={form} />
      <StorageClaimSection form={form} />
      <DisruptionBudgetSection form={form} />
      <NetworkPolicySection form={form} />
      <SecretSection form={form} />
      <RoleSection form={form} />
      <RoleBindingSection form={form} />
      <GatewaySection form={form} />
      <AdvancedFieldsSection form={form} />
    </div>
  );
}
