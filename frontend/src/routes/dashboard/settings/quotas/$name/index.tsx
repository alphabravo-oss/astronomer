import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/quotas/[name] — edit a single quota plan.
 *
 * The backend's write contract uses snake_case (matching the Go `json:"..."`
 * tags). We keep camelCase on the client for ergonomics and convert at the
 * boundary in `handleSave`. Enforcement is the only enum; everything else is
 * an integer cap.
 */
import { useEffect, useId, useState } from "react";
import { Link } from "@/lib/link";
import { useParams, useRouter } from "@/lib/navigation";
import { ArrowLeft, Gauge, Loader2, Save, Trash2 } from "lucide-react";
import { toastError } from "@/lib/toast";
import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import {
  useDeleteQuotaPlan,
  useQuotaPlan,
  useUpdateQuotaPlan,
} from "@/components/settings/hooks";
import type {
  QuotaEnforcement,
  QuotaPlanView,
  QuotaPlanWriteRequest,
} from "@/lib/api/quotas";

function toWrite(form: QuotaPlanView): QuotaPlanWriteRequest {
  return {
    name: form.name,
    description: form.description,
    enforcement: form.enforcement,
    max_clusters_per_project: form.maxClustersPerProject,
    max_namespaces_per_project: form.maxNamespacesPerProject,
    max_members_per_project: form.maxMembersPerProject,
    max_projects_per_user: form.maxProjectsPerUser,
    max_tokens_per_user: form.maxTokensPerUser,
    max_streams_per_user: form.maxStreamsPerUser,
    max_total_clusters: form.maxTotalClusters,
    max_total_users: form.maxTotalUsers,
  };
}

function NumberField({
  label,
  value,
  onChange,
  hint,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
  hint?: string;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <label htmlFor={id} className="text-sm font-medium text-foreground">
        {label}
      </label>
      <Input
        id={id}
        type="number"
        value={value}
        min={0}
        onChange={(e) => onChange(Number(e.target.value))}
      />
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

function QuotaPlanForm({ initial }: { initial: QuotaPlanView }) {
  const router = useRouter();
  const update = useUpdateQuotaPlan();
  const del = useDeleteQuotaPlan();
  const [form, setForm] = useState<QuotaPlanView>(initial);
  const [confirmDelete, setConfirmDelete] = useState(false);

  useEffect(() => {
    setForm(initial);
  }, [initial]);

  const dirty = JSON.stringify(form) !== JSON.stringify(initial);

  const handleSave = async () => {
    try {
      await update.mutateAsync({ name: form.name, body: toWrite(form) });
    } catch {
      // toast handled
    }
  };

  return (
    <div className="space-y-6">
      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <h2 className="text-base font-semibold text-foreground">
          Identification
        </h2>
        <div className="grid grid-cols-1 gap-4">
          <div className="space-y-1.5">
            <label
              className="text-sm font-medium text-foreground"
              htmlFor="field-7cc41abf-110"
            >
              Name
            </label>
            <Input
              id="field-7cc41abf-110"
              type="text"
              value={form.name}
              disabled
              className="bg-muted font-mono text-muted-foreground"
            />
            <p className="text-xs text-muted-foreground">
              Plan name is immutable.
            </p>
          </div>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-7cc41abf-129"
          >
            Description
          </label>
          <Textarea
            id="field-7cc41abf-129"
            value={form.description ?? ""}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            rows={2}
            className="min-h-0 text-sm font-sans"
          />
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-7cc41abf-138"
          >
            Enforcement
          </label>
          <Select
            id="field-7cc41abf-138"
            value={form.enforcement}
            onChange={(e) =>
              setForm({
                ...form,
                enforcement: e.target.value as QuotaEnforcement,
              })
            }
          >
            <option value="hard">Hard — reject writes over cap</option>
            <option value="soft">Soft — warn but allow</option>
          </Select>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6 space-y-4">
        <h2 className="text-base font-semibold text-foreground">Limits</h2>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <NumberField
            label="Max clusters per project"
            value={form.maxClustersPerProject}
            onChange={(v) => setForm({ ...form, maxClustersPerProject: v })}
          />
          <NumberField
            label="Max namespaces per project"
            value={form.maxNamespacesPerProject}
            onChange={(v) =>
              setForm({ ...form, maxNamespacesPerProject: v })
            }
          />
          <NumberField
            label="Max members per project"
            value={form.maxMembersPerProject}
            onChange={(v) => setForm({ ...form, maxMembersPerProject: v })}
          />
          <NumberField
            label="Max projects per user"
            value={form.maxProjectsPerUser}
            onChange={(v) => setForm({ ...form, maxProjectsPerUser: v })}
          />
          <NumberField
            label="Max API tokens per user"
            value={form.maxTokensPerUser}
            onChange={(v) => setForm({ ...form, maxTokensPerUser: v })}
          />
          <NumberField
            label="Max concurrent streams per user"
            value={form.maxStreamsPerUser}
            onChange={(v) => setForm({ ...form, maxStreamsPerUser: v })}
          />
          <NumberField
            label="Fleet cluster cap"
            value={form.maxTotalClusters}
            onChange={(v) => setForm({ ...form, maxTotalClusters: v })}
          />
          <NumberField
            label="Fleet active-user cap"
            value={form.maxTotalUsers}
            onChange={(v) => setForm({ ...form, maxTotalUsers: v })}
          />
        </div>
        <p className="text-xs text-muted-foreground">
          Use <span className="font-mono">0</span> to mean unlimited.
        </p>
      </div>

      <div className="flex items-center justify-between sticky bottom-4 z-10 rounded-xl border border-border bg-popover/80 backdrop-blur p-3 shadow-sm">
        <ActionButton
          intent="destructive"
          icon={<Trash2 className="h-3.5 w-3.5" />}
          onClick={() => setConfirmDelete(true)}
        >
          Delete plan
        </ActionButton>
        <div className="flex items-center gap-3">
          <p className="text-xs text-muted-foreground">
            {dirty ? "Unsaved changes" : "Saved"}
          </p>
          <ActionButton
            intent="primary"
            onClick={handleSave}
            disabled={!dirty || update.isPending}
            loading={update.isPending}
            icon={<Save className="h-3.5 w-3.5" />}
          >
            Save changes
          </ActionButton>
        </div>
      </div>

      <ConfirmDialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={async () => {
          await del.mutateAsync(form.name);
          router.push("/dashboard/settings/quotas");
        }}
        title="Delete quota plan?"
        description={`Deleting "${form.name}" only works if no tenant is currently bound to this plan.`}
        confirmText="Delete"
        variant="destructive"
      />
    </div>
  );
}

function QuotaPlanInner() {
  const params = useParams<{ name: string }>();
  const name = params?.name ? decodeURIComponent(params.name) : undefined;
  const { data, isLoading, error } = useQuotaPlan(name);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (error || !data) {
    toastError("Failed to load quota plan");
    return (
      <EmptyState
        icon={Gauge}
        title="Quota plan not found"
        description="The quota plan may have been deleted or renamed."
        actionLabel="Back to quotas"
        actionHref="/dashboard/settings/quotas"
      />
    );
  }
  return <QuotaPlanForm initial={data} />;
}

function QuotaPlanDetailPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <Link
          href="/dashboard/settings/quotas"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to quotas
        </Link>
        <PageHeader
          eyebrow="Settings · Quota plan"
          title={
            <span className="flex items-center gap-2">
              <Gauge className="h-5 w-5 text-muted-foreground" />
              Edit plan
            </span>
          }
        />
        <QuotaPlanInner />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/quotas/$name/")({
  component: QuotaPlanDetailPage,
});
