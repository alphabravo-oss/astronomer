import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/general/tokens/new — mint an API token.
 *
 * The token secret is shown exactly once on successful creation, so this
 * page swaps from the create form to a "copy it now" reveal state rather
 * than navigating away automatically.
 */
import { useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, Key } from "lucide-react";
import { useCreateAPIToken } from "@/lib/hooks/user-settings";
import { toastError } from "@/lib/toast";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code-block";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { PageHeader, PageShell } from "@/components/ui/page";

function CreatedTokenPanel({ token }: { token: string }) {
  const navigate = useNavigate();
  return (
    <Card radius="xl" padding="lg" className="space-y-4">
      <p className="text-sm text-status-warning">
        Copy this token now. You will not be able to see it again.
      </p>
      <CodeBlock code={token} title="API Token" />
      <div className="flex justify-end">
        <ActionButton
          intent="primary"
          onClick={() =>
            void navigate({
              to: "/dashboard/settings/general",
              search: { tab: "tokens" },
            })
          }
        >
          Done
        </ActionButton>
      </div>
    </Card>
  );
}

function NewTokenForm() {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [expiresInDays, setExpiresInDays] = useState(30);
  const [createdToken, setCreatedToken] = useState<string | null>(null);
  const createToken = useCreateAPIToken();

  const handleCreateToken = async () => {
    if (!name) {
      toastError("Token name is required");
      return;
    }
    try {
      const result = await createToken.mutateAsync({ name, expiresInDays });
      setCreatedToken(result.token);
    } catch {
      // Error handled by mutation
    }
  };

  if (createdToken) {
    return <CreatedTokenPanel token={createdToken} />;
  }

  return (
    <div className="space-y-6">
      <Card radius="xl" padding="lg" className="space-y-4">
        <div className="space-y-1.5">
          <label htmlFor="token-name" className="text-sm font-medium text-foreground">
            Token Name
          </label>
          <Input
            id="token-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g., CI/CD Pipeline"
            data-initial-focus
          />
        </div>
        <div className="space-y-1.5">
          <label
            htmlFor="token-expires"
            className="text-sm font-medium text-foreground"
          >
            Expires In
          </label>
          <Select
            id="token-expires"
            value={expiresInDays}
            onChange={(e) => setExpiresInDays(Number(e.target.value))}
          >
            <option value={7}>7 days</option>
            <option value={30}>30 days</option>
            <option value={90}>90 days</option>
            <option value={365}>1 year</option>
            <option value={0}>Never</option>
          </Select>
        </div>
      </Card>
      <div className="flex items-center justify-end gap-2">
        <ActionButton
          onClick={() =>
            void navigate({
              to: "/dashboard/settings/general",
              search: { tab: "tokens" },
            })
          }
        >
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={() => void handleCreateToken()}
          disabled={!name}
          loading={createToken.isPending}
        >
          Create Token
        </ActionButton>
      </div>
    </div>
  );
}

function NewTokenPage() {
  return (
    <PageShell>
      <RouterLink
        to="/dashboard/settings/general"
        search={{ tab: "tokens" }}
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to API tokens
      </RouterLink>
      <PageHeader
        eyebrow="Settings · General · New"
        title={
          <span className="flex items-center gap-2">
            <Key className="h-5 w-5 text-muted-foreground" />
            Create API token
          </span>
        }
      />
      <NewTokenForm />
    </PageShell>
  );
}

export const Route = createFileRoute(
  "/dashboard/settings/general/tokens/new/",
)({
  component: NewTokenPage,
});
