import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/auth/scim-tokens/new — mint a SCIM provisioning
 * token. The plaintext secret is shown exactly once on success, so this
 * page swaps to a reveal state instead of navigating away automatically.
 */
import { useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, Check, Copy, KeyRound, ShieldAlert } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { toastSuccess } from "@/lib/toast";
import type { SCIMTokenCreated } from "@/types";
import { useCreateSCIMToken } from "../-hooks";

function CreatedTokenPanel({ created }: { created: SCIMTokenCreated }) {
  const navigate = useNavigate();
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(created.token);
      setCopied(true);
      toastSuccess("Token copied to clipboard");
      setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard blocked — operator can select the text manually */
    }
  };

  return (
    <Card radius="xl" padding="lg" className="space-y-4">
      <div className="flex items-start gap-2 rounded-lg border border-status-warning/30 bg-status-warning/10 p-3">
        <ShieldAlert className="h-4 w-4 text-status-warning shrink-0 mt-0.5" />
        <p className="text-xs text-foreground">
          Copy this token now — it is shown <b>only once</b>. Only its hash is
          stored; it cannot be recovered later.
        </p>
      </div>
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">
          {created.name}
        </label>
        <div className="flex items-center gap-2">
          <code className="flex-1 px-3 py-2 rounded-md border border-border bg-background text-xs font-mono text-foreground break-all">
            {created.token}
          </code>
          <ActionButton
            onClick={() => void copy()}
            size="icon"
            title="Copy token"
            icon={
              copied ? (
                <Check className="h-4 w-4 text-status-success" />
              ) : (
                <Copy className="h-4 w-4" />
              )
            }
          />
        </div>
      </div>
      <div className="flex justify-end">
        <ActionButton
          intent="primary"
          onClick={() =>
            void navigate({ to: "/dashboard/settings/auth/scim-tokens" })
          }
        >
          Done
        </ActionButton>
      </div>
    </Card>
  );
}

function NewSCIMTokenForm() {
  const navigate = useNavigate();
  const create = useCreateSCIMToken();
  const [name, setName] = useState("");
  const [created, setCreated] = useState<SCIMTokenCreated | null>(null);

  const handleCreate = async () => {
    try {
      const t = await create.mutateAsync(name.trim());
      setCreated(t);
    } catch {
      /* mutation toasts on error */
    }
  };

  if (created) {
    return <CreatedTokenPanel created={created} />;
  }

  return (
    <div className="space-y-6">
      <Card radius="xl" padding="lg" className="space-y-1.5">
        <label className="text-sm font-medium text-foreground" htmlFor="scim-token-name">
          Name
        </label>
        <Input
          id="scim-token-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="okta-provisioning"
          data-initial-focus
        />
        <p className="text-2xs text-muted-foreground">
          A label to recognize this token. The secret is shown once on the next
          screen.
        </p>
      </Card>
      <div className="flex items-center justify-end gap-2">
        <ActionButton
          onClick={() =>
            void navigate({ to: "/dashboard/settings/auth/scim-tokens" })
          }
        >
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={() => void handleCreate()}
          disabled={create.isPending || !name.trim()}
          loading={create.isPending}
        >
          Mint Token
        </ActionButton>
      </div>
    </div>
  );
}

function NewSCIMTokenPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings/auth/scim-tokens"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to SCIM tokens
        </RouterLink>
        <PageHeader
          eyebrow="Settings · Auth · SCIM · New"
          title={
            <span className="flex items-center gap-2">
              <KeyRound className="h-5 w-5 text-muted-foreground" />
              Mint SCIM token
            </span>
          }
        />
        <NewSCIMTokenForm />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute(
  "/dashboard/settings/auth/scim-tokens/new/",
)({
  component: NewSCIMTokenPage,
});
