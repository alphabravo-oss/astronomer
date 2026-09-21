import { useAppForm, useStore } from "@/lib/form";
import { useAssignSecurityPolicy } from "@/lib/hooks/security";
import { useClusters } from "@/lib/hooks/clusters";
import { ModalShell } from "@/components/ui/modal-shell";
import { ActionButton } from "@/components/ui/action-button";
import { cn } from "@/lib/utils";
import type { PodSecurityTemplate } from "@/types";
import { psaLevelColors } from "./-psa-constants";

// ============================================================
// Assign Template Modal — unchanged from Phase A2
// ============================================================

export function AssignTemplateModal({
  templates,
  onClose,
}: {
  templates: PodSecurityTemplate[];
  onClose: () => void;
}) {
  const assignPolicy = useAssignSecurityPolicy();
  const { data: clustersData } = useClusters({ pageSize: 100 });
  const clusters = clustersData?.data || [];

  const form = useAppForm({
    defaultValues: {
      clusterId: "",
      templateId:
        templates.find((t) => t.isDefault)?.id || templates[0]?.id || "",
    },
    validators: {
      onSubmit: ({ value }) =>
        !value.clusterId || !value.templateId
          ? "Select a cluster and security template."
          : undefined,
    },
    onSubmit: async ({ value }) => {
      try {
        await assignPolicy.mutateAsync({
          cluster_id: value.clusterId,
          template_id: value.templateId,
        });
        onClose();
      } catch {
        // Persistent failure is rendered in the form summary.
      }
    },
  });
  const values = useStore(form.store, (state) => state.values);
  const selectedTemplate = templates.find(
    (template) => template.id === values.templateId,
  );

  return (
    <ModalShell
      title="Assign Security Template"
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={
              assignPolicy.isPending || !values.clusterId || !values.templateId
            }
            loading={assignPolicy.isPending}
          >
            Assign Template
          </ActionButton>
        </>
      }
    >
      <form.AppForm>
        <form.FormErrorSummary serverError={assignPolicy.error?.message} />
      </form.AppForm>
      <form.AppField name="clusterId">
        {(field) => (
          <field.SelectField label="Cluster">
            <option value="">Select a cluster…</option>
            {clusters.map((cluster) => (
              <option key={cluster.id} value={cluster.id}>
                {cluster.displayName} ({cluster.name})
              </option>
            ))}
          </field.SelectField>
        )}
      </form.AppField>
      <form.AppField name="templateId">
        {(field) => (
          <field.SelectField label="Template">
            {templates.map((template) => (
              <option key={template.id} value={template.id}>
                {template.name}
                {template.isDefault ? " (Default)" : ""}
              </option>
            ))}
          </field.SelectField>
        )}
      </form.AppField>

      {selectedTemplate && (
        <div className="rounded-lg border border-border bg-muted/30 p-4 space-y-2">
          <p className="text-xs font-medium text-muted-foreground">
            Template Preview
          </p>
          <div className="grid grid-cols-3 gap-3">
            <div>
              <p className="text-2xs text-muted-foreground">Enforce</p>
              <span
                className={cn(
                  "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
                  psaLevelColors[selectedTemplate.enforceLevel],
                )}
              >
                {selectedTemplate.enforceLevel}
              </span>
            </div>
            <div>
              <p className="text-2xs text-muted-foreground">Audit</p>
              <span
                className={cn(
                  "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
                  psaLevelColors[selectedTemplate.auditLevel],
                )}
              >
                {selectedTemplate.auditLevel}
              </span>
            </div>
            <div>
              <p className="text-2xs text-muted-foreground">Warn</p>
              <span
                className={cn(
                  "text-xs px-2 py-0.5 rounded-sm font-medium capitalize",
                  psaLevelColors[selectedTemplate.warnLevel],
                )}
              >
                {selectedTemplate.warnLevel}
              </span>
            </div>
          </div>
          {selectedTemplate.description && (
            <p className="text-xs text-muted-foreground">
              {selectedTemplate.description}
            </p>
          )}
        </div>
      )}
    </ModalShell>
  );
}
