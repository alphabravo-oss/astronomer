import { useAppForm, useStore } from "@/lib/form";
import {
  useCreatePodSecurityTemplate,
  useUpdatePodSecurityTemplate,
} from "@/lib/hooks/security";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import type { PodSecurityLevel, PodSecurityTemplate } from "@/types";
import { psaLevels } from "./-psa-constants";
import { LabeledSelectField, LabeledTextField } from "./-psa-template-fields";

// ============================================================
// PSA Template Modal — unchanged from Phase A2
// ============================================================

const LEVEL_FIELDS = [
  { name: "enforceLevel", id: "field-7db9bce2-715", label: "Enforce" },
  { name: "auditLevel", id: "field-7db9bce2-734", label: "Audit" },
  { name: "warnLevel", id: "field-7db9bce2-753", label: "Warn" },
] as const;

const VERSION_FIELDS = [
  { name: "enforceVersion", id: "field-7db9bce2-775", label: "Enforce Version" },
  { name: "auditVersion", id: "field-7db9bce2-791", label: "Audit Version" },
  { name: "warnVersion", id: "field-7db9bce2-806", label: "Warn Version" },
] as const;

const EXEMPTION_FIELDS = [
  {
    name: "exemptNamespaces",
    id: "field-7db9bce2-826",
    label: "Namespaces (comma-separated)",
    placeholder: "kube-system, kube-public, kube-node-lease",
  },
  {
    name: "exemptRuntimeClasses",
    id: "field-7db9bce2-842",
    label: "Runtime Classes (comma-separated)",
    placeholder: "gvisor, kata",
  },
  {
    name: "exemptUsernames",
    id: "field-7db9bce2-858",
    label: "Usernames (comma-separated)",
    placeholder: "system:serviceaccount:kube-system:default",
  },
] as const;

function parseCSV(val: string): string[] {
  return val
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function PSATemplateModal({
  template,
  onClose,
}: {
  template: PodSecurityTemplate | null;
  onClose: () => void;
}) {
  const createTemplate = useCreatePodSecurityTemplate();
  const updateTemplate = useUpdatePodSecurityTemplate();

  const form = useAppForm({
    defaultValues: {
      name: template?.name || "",
      description: template?.description || "",
      enforceLevel: template?.enforceLevel || ("baseline" as PodSecurityLevel),
      enforceVersion: template?.enforceVersion || "latest",
      auditLevel: template?.auditLevel || ("restricted" as PodSecurityLevel),
      auditVersion: template?.auditVersion || "latest",
      warnLevel: template?.warnLevel || ("restricted" as PodSecurityLevel),
      warnVersion: template?.warnVersion || "latest",
      exemptNamespaces: template?.exemptNamespaces?.join(", ") || "",
      exemptRuntimeClasses: template?.exemptRuntimeClasses?.join(", ") || "",
      exemptUsernames: template?.exemptUsernames?.join(", ") || "",
    },
    onSubmit: async ({ value }) => {
      const data = {
        name: value.name,
        description: value.description || undefined,
        enforceLevel: value.enforceLevel,
        enforceVersion: value.enforceVersion || undefined,
        auditLevel: value.auditLevel,
        auditVersion: value.auditVersion || undefined,
        warnLevel: value.warnLevel,
        warnVersion: value.warnVersion || undefined,
        exemptNamespaces: parseCSV(value.exemptNamespaces),
        exemptRuntimeClasses: parseCSV(value.exemptRuntimeClasses),
        exemptUsernames: parseCSV(value.exemptUsernames),
      };

      try {
        if (template) {
          await updateTemplate.mutateAsync({ id: template.id, data });
        } else {
          await createTemplate.mutateAsync(data);
        }
        onClose();
      } catch {
        // Error surfaced by mutation toast.
      }
    },
  });

  const templateName = useStore(form.store, (s) => s.values.name);
  const isPending = createTemplate.isPending || updateTemplate.isPending;

  return (
    <ModalShell
      title={template ? "Edit PSA Template" : "Create PSA Template"}
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={isPending || !templateName}
            loading={isPending}
          >
            {template ? "Update Template" : "Create Template"}
          </ActionButton>
        </>
      }
    >
      <form.AppForm>
        <form.FormErrorSummary
          serverError={
            createTemplate.error?.message ?? updateTemplate.error?.message
          }
        />
      </form.AppForm>

      <form.Field name="name">
        {(field) => (
          <LabeledTextField
            field={field}
            id="field-7db9bce2-682"
            label="Name"
            placeholder="restricted-production"
          />
        )}
      </form.Field>

      <form.Field name="description">
        {(field) => (
          <LabeledTextField
            field={field}
            id="field-7db9bce2-698"
            label="Description"
            placeholder="Restricted policy for production clusters"
          />
        )}
      </form.Field>

      <div className="grid grid-cols-3 gap-4">
        {LEVEL_FIELDS.map(({ name, id, label }) => (
          <form.Field key={name} name={name}>
            {(field) => (
              <LabeledSelectField
                field={field}
                id={id}
                label={label}
                options={psaLevels}
              />
            )}
          </form.Field>
        ))}
      </div>

      <div className="grid grid-cols-3 gap-4">
        {VERSION_FIELDS.map(({ name, id, label }) => (
          <form.Field key={name} name={name}>
            {(field) => (
              <LabeledTextField
                field={field}
                id={id}
                label={label}
                placeholder="latest"
                className="h-8 text-xs"
                small
              />
            )}
          </form.Field>
        ))}
      </div>

      <div className="space-y-3 pt-2">
        <p className="text-sm font-medium text-foreground">Exemptions</p>
        {EXEMPTION_FIELDS.map(({ name, id, label, placeholder }) => (
          <form.Field key={name} name={name}>
            {(field) => (
              <LabeledTextField
                field={field}
                id={id}
                label={label}
                placeholder={placeholder}
                small
              />
            )}
          </form.Field>
        ))}
      </div>
    </ModalShell>
  );
}
