import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { FormShell } from "@/components/ui/form-shell";
import { useAppForm } from "@/lib/form";
import { queryKeys } from "@/lib/query-keys";
import { ActionButton as Button } from "@/components/ui/action-button";
import {
  getSettingsIdentityBindings,
  postSettingsIdentityBindings,
  deleteSettingsIdentityBindings,
} from "@/lib/api/generated/client";
import { toastError } from "@/lib/toast";
import { useSSOProviders } from "@/lib/hooks/user-settings";

type Binding = { issuer: string; subject: string; user_id: string };
function IdentitySettings() {
  const { data: providers } = useSSOProviders();
  const enabled = providers?.some((p) => p.enabled) ?? false;
  const [offset, setOffset] = useState(0);
  const cache = useQueryClient();
  const bindings = useQuery({
    queryKey: queryKeys.settings.identityBindings(offset),
    enabled,
    queryFn: async () => {
      const r = await getSettingsIdentityBindings({ query: { offset } });
      return (r.data ?? []) as Binding[];
    },
  });
  const change = useMutation({
    mutationFn: async ({
      method,
      user_id,
      subject,
    }: {
      method: "post" | "delete";
      user_id: string;
      subject: string;
    }) =>
      (method === "post"
        ? postSettingsIdentityBindings
        : deleteSettingsIdentityBindings)({ body: { user_id, subject } }),
    onError: () =>
      toastError(
        "The identity binding could not be changed. Check the account, subject and existing bindings.",
      ),
    onSuccess: () => {
      void cache.invalidateQueries({
        queryKey: queryKeys.settings.identityBindingsAll,
      });
    },
  });
  const form = useAppForm({
    defaultValues: { user_id: "", subject: "" },
    onSubmit: async ({ value }) => {
      try {
        await change.mutateAsync({ method: "post", ...value });
        form.reset();
      } catch {
        /* Mutation state renders the error. */
      }
    },
  });
  return (
    <PageShell>
      <PageHeader
        title="Authentication"
        description="Local login is always available. Single sign-on is optional."
      />
      <section className="space-y-4">
        <h2 className="text-lg font-semibold">Local accounts</h2>
        <p>
          Password login, MFA, recovery and API tokens remain available
          regardless of SSO.
        </p>
        <h2 className="text-lg font-semibold">Single sign-on</h2>
        <p>
          {enabled
            ? "An external identity provider is configured."
            : "Single sign-on is not configured. Configure a trusted provider in the deployment settings to enable it."}
        </p>
        {enabled && (
          <>
            <p>
              Link an existing account using the exact subject supplied by your
              identity provider. Email addresses never link accounts
              automatically.
            </p>
            <FormShell
              form={form}
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                if (!change.isPending) void form.handleSubmit();
              }}
            >
              <form.AppForm>
                <form.FormErrorSummary
                  serverError={
                    change.isError
                      ? "The identity binding could not be changed. Check the account, subject and existing bindings."
                      : undefined
                  }
                />
              </form.AppForm>
              <form.AppField name="user_id">
                {(field) => <field.TextField label="Local user ID" required />}
              </form.AppField>
              <form.AppField name="subject">
                {(field) => (
                  <field.TextField label="External subject" required />
                )}
              </form.AppField>
              <Button intent="primary" type="submit" loading={change.isPending}>
                Link account
              </Button>
            </FormShell>
            {change.isError && (
              <p role="alert">
                The binding could not be changed. Check the account, subject and
                existing bindings.
              </p>
            )}
            {bindings.isError && (
              <p role="alert">Could not load identity bindings.</p>
            )}
            <ul className="space-y-3">
              {bindings.data?.map((b) => (
                <li key={b.subject} className="flex items-center gap-3">
                  <span>
                    {b.subject} → {b.user_id}
                  </span>
                  <Button
                    intent="default"
                    disabled={change.isPending}
                    onClick={() =>
                      change.mutate({
                        method: "delete",
                        user_id: b.user_id,
                        subject: b.subject,
                      })
                    }
                  >
                    Unlink
                  </Button>
                </li>
              ))}
            </ul>
            <div className="flex gap-3">
              <Button
                intent="default"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - 100))}
              >
                Previous
              </Button>
              <Button
                intent="default"
                disabled={(bindings.data?.length ?? 0) < 100}
                onClick={() => setOffset(offset + 100)}
              >
                Next
              </Button>
            </div>
          </>
        )}
      </section>
    </PageShell>
  );
}
export const Route = createFileRoute("/dashboard/settings/auth/")({
  component: () => (
    <SettingsAuthGate>
      <IdentitySettings />
    </SettingsAuthGate>
  ),
});
