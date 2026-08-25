import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/quotas/new — create a new quota plan.
 *
 * Shares the same field set as the detail page; only the name field is
 * editable on this surface (it becomes the immutable URL key once saved).
 */
import { useId, useState } from "react";
import { Link } from "@/lib/link";
import { useRouter } from "@/lib/navigation";
import { ArrowLeft, Gauge, Save } from "lucide-react";
import { toastError } from "@/lib/toast";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useCreateQuotaPlan } from "@/components/settings/hooks";
import type {
  QuotaEnforcement,
  QuotaPlanWriteRequest,
} from "@/lib/api/quotas";

const DEFAULT_FORM: QuotaPlanWriteRequest = {
  name: "",
  description: "",
  enforcement: "soft",
  max_clusters_per_project: 5,
  max_namespaces_per_project: 50,
  max_members_per_project: 25,
  max_projects_per_user: 10,
  max_tokens_per_user: 25,
  max_streams_per_user: 5,
  max_total_clusters: 0,
  max_total_users: 0,
};

function NumberField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (v: number) => void;
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
    </div>
  );
}

function NewQuotaPlanForm() {
  const router = useRouter();
  const create = useCreateQuotaPlan();
  const [form, setForm] = useState<QuotaPlanWriteRequest>(DEFAULT_FORM);

  const handleCreate = async () => {
    if (!form.name) {
      toastError("Plan name is required");
      return;
    }
    if (!/^[a-z0-9][a-z0-9-]*$/.test(form.name)) {
      toastError("Plan name must be lowercase letters, numbers, and dashes");
      return;
    }
    try {
      const created = await create.mutateAsync(form);
      router.push(
        `/dashboard/settings/quotas/${encodeURIComponent(created.name)}`,
      );
    } catch {
      // mutation toasts
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
              htmlFor="field-71779899-92"
            >
              Name
            </label>
            <Input
              id="field-71779899-92"
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="enterprise-tier"
              className="font-mono"
              data-initial-focus
            />
            <p className="text-xs text-muted-foreground">
              Lowercase, numbers, dashes. This becomes the immutable URL key.
            </p>
          </div>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-71779899-116"
          >
            Description
          </label>
          <Textarea
            id="field-71779899-116"
            value={form.description ?? ""}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            rows={2}
            className="min-h-0 text-sm font-sans"
          />
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-71779899-125"
          >
            Enforcement
          </label>
          <Select
            id="field-71779899-125"
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
            value={form.max_clusters_per_project}
            onChange={(v) =>
              setForm({ ...form, max_clusters_per_project: v })
            }
          />
          <NumberField
            label="Max namespaces per project"
            value={form.max_namespaces_per_project}
            onChange={(v) =>
              setForm({ ...form, max_namespaces_per_project: v })
            }
          />
          <NumberField
            label="Max members per project"
            value={form.max_members_per_project}
            onChange={(v) =>
              setForm({ ...form, max_members_per_project: v })
            }
          />
          <NumberField
            label="Max projects per user"
            value={form.max_projects_per_user}
            onChange={(v) =>
              setForm({ ...form, max_projects_per_user: v })
            }
          />
          <NumberField
            label="Max API tokens per user"
            value={form.max_tokens_per_user}
            onChange={(v) =>
              setForm({ ...form, max_tokens_per_user: v })
            }
          />
          <NumberField
            label="Max concurrent streams per user"
            value={form.max_streams_per_user}
            onChange={(v) =>
              setForm({ ...form, max_streams_per_user: v })
            }
          />
          <NumberField
            label="Fleet cluster cap"
            value={form.max_total_clusters}
            onChange={(v) => setForm({ ...form, max_total_clusters: v })}
          />
          <NumberField
            label="Fleet active-user cap"
            value={form.max_total_users}
            onChange={(v) => setForm({ ...form, max_total_users: v })}
          />
        </div>
      </div>

      <div className="flex items-center justify-end gap-2">
        <ActionButton onClick={() => router.push("/dashboard/settings/quotas")}>
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={handleCreate}
          loading={create.isPending}
          icon={<Save className="h-3.5 w-3.5" />}
        >
          Create plan
        </ActionButton>
      </div>
    </div>
  );
}

function NewQuotaPlanPage() {
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
          eyebrow="Settings · Quotas · New"
          title={
            <span className="flex items-center gap-2">
              <Gauge className="h-5 w-5 text-muted-foreground" />
              New quota plan
            </span>
          }
        />
        <NewQuotaPlanForm />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/quotas/new/")({
  component: NewQuotaPlanPage,
});
