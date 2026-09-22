import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/read-audit/new — create a read-side audit policy.
 * See settings/read-audit/index.tsx for the list + delete flow.
 */
import { useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ShieldCheck } from "lucide-react";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { PageHeader, PageShell } from "@/components/ui/page";
import { createReadAuditPolicy } from "@/lib/api/settings";

function NewReadAuditPolicyForm() {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [pathPattern, setPathPattern] = useState("");
  const [verbs, setVerbs] = useState("GET");
  const [sampleRate, setSampleRate] = useState(1);
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      await createReadAuditPolicy({
        name,
        description,
        path_pattern: pathPattern,
        verbs,
        sample_rate: sampleRate,
        enabled,
      });
      void navigate({ to: "/dashboard/settings/read-audit" });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      <Card radius="xl" padding="lg" className="space-y-4">
        {error && (
          <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-sm text-destructive">
            {error}
          </div>
        )}
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="new-policy-name"
          >
            Name
          </label>
          <Input
            id="new-policy-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            data-initial-focus
          />
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="new-policy-description"
          >
            Description
          </label>
          <Input
            id="new-policy-description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="new-policy-path"
          >
            Path pattern (e.g. /admin/sso or /projects/*/cloud-credentials)
          </label>
          <Input
            id="new-policy-path"
            value={pathPattern}
            onChange={(e) => setPathPattern(e.target.value)}
            className="font-mono"
          />
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="new-policy-verbs"
          >
            Verbs (comma-separated or *)
          </label>
          <Input
            id="new-policy-verbs"
            value={verbs}
            onChange={(e) => setVerbs(e.target.value)}
          />
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="new-policy-sample-rate"
          >
            Sample rate: {Math.round(sampleRate * 100)}%
          </label>
          <Input
            id="new-policy-sample-rate"
            type="range"
            min={0}
            max={1}
            step={0.05}
            value={sampleRate}
            onChange={(e) => setSampleRate(Number(e.target.value))}
            className="w-full"
          />
        </div>
        <div className="flex items-center gap-2">
          <Switch
            id="new-policy-enabled"
            checked={enabled}
            onCheckedChange={setEnabled}
          />
          <label
            htmlFor="new-policy-enabled"
            className="text-sm text-foreground"
          >
            Enabled
          </label>
        </div>
      </Card>

      <div className="flex items-center justify-end gap-2">
        <ActionButton
          onClick={() =>
            void navigate({ to: "/dashboard/settings/read-audit" })
          }
        >
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={() => void submit()}
          disabled={busy || !name || !pathPattern}
          loading={busy}
          loadingLabel="Creating…"
        >
          Create
        </ActionButton>
      </div>
    </div>
  );
}

function NewReadAuditPolicyPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings/read-audit"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to read-side audit policies
        </RouterLink>
        <PageHeader
          eyebrow="Settings · Read-side audit · New"
          title={
            <span className="flex items-center gap-2">
              <ShieldCheck className="h-5 w-5 text-muted-foreground" />
              New read-audit policy
            </span>
          }
        />
        <NewReadAuditPolicyForm />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/read-audit/new/")({
  component: NewReadAuditPolicyPage,
});
