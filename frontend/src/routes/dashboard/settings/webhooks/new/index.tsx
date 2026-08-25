import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/webhooks/new — three-step wizard.
 *
 *   1. Pick template — card grid: Slack / PagerDuty / Generic.
 *   2. Configure — name, URL, signing secret, event/severity filters.
 *   3. Preview — JSON sample of the outbound payload the operator is about
 *      to wire up. Confirming creates the subscription and pushes them to
 *      the detail page.
 */
import { useState } from "react";
import { Link } from "@/lib/link";
import { useAppForm, useStore } from "@/lib/form";
import { useRouter } from "@/lib/navigation";
import { ArrowLeft, Hash, Send, Settings2, Webhook } from "lucide-react";
import { toastError } from "@/lib/toast";
import { cn } from "@/lib/utils";
import { ActionButton } from "@/components/ui/action-button";
import { CodeBlock } from "@/components/ui/code-block";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { useCreateWebhook } from "@/components/settings/hooks";
import type { WebhookFilter, WebhookTemplate } from "@/lib/api/settings";

type Step = "pick" | "configure" | "preview";

interface TemplateMeta {
  template: WebhookTemplate;
  label: string;
  description: string;
  icon: React.ElementType;
  urlPlaceholder: string;
  samplePayload: Record<string, unknown>;
}

const TEMPLATES: TemplateMeta[] = [
  {
    template: "slack",
    label: "Slack",
    description: "Incoming-webhook URL. Renders events as Slack blocks.",
    icon: Hash,
    urlPlaceholder: "https://hooks.slack.com/services/T.../B.../...",
    samplePayload: {
      text: "cluster.unhealthy — prod-east",
      blocks: [
        {
          type: "section",
          text: { type: "mrkdwn", text: "*Cluster unhealthy* — `prod-east`" },
        },
        {
          type: "context",
          elements: [
            {
              type: "mrkdwn",
              text: "Severity: warning · 2026-05-12T18:00:00Z",
            },
          ],
        },
      ],
    },
  },
  {
    template: "pagerduty",
    label: "PagerDuty",
    description:
      "Events API v2 routing key. Severity maps to incident severity.",
    icon: Send,
    urlPlaceholder: "https://events.pagerduty.com/v2/enqueue",
    samplePayload: {
      routing_key: "<routing-key>",
      event_action: "trigger",
      payload: {
        summary: "cluster.unhealthy — prod-east",
        severity: "warning",
        source: "astronomer",
      },
    },
  },
  {
    template: "generic",
    label: "Generic JSON",
    description: "Raw JSON POST with an HMAC-SHA256 signature header.",
    icon: Webhook,
    urlPlaceholder: "https://example.com/hooks/astronomer",
    samplePayload: {
      event: "cluster.unhealthy",
      severity: "warning",
      timestamp: "2026-05-12T18:00:00Z",
      cluster: { id: "...", name: "prod-east" },
      detail: { message: "apiserver unreachable" },
    },
  },
];

const AVAILABLE_EVENTS = [
  "cluster.unhealthy",
  "cluster.healthy",
  "backup.failed",
  "backup.succeeded",
  "project.created",
  "project.deleted",
  "auth.failed",
  "auth.locked",
  "quota.exceeded",
];

function NewWebhookWizard() {
  const router = useRouter();
  const createMutation = useCreateWebhook();

  const [step, setStep] = useState<Step>("pick");
  const [selected, setSelected] = useState<TemplateMeta | null>(null);

  const form = useAppForm({
    defaultValues: {
      name: "",
      url: "",
      secret: "",
      events: [] as string[],
    },
    validators: {
      // Old check (imperative, pre-submit): `if (!name || !url)` → ported 1:1
      // as a form-level onSubmit validator.
      onSubmit: ({ value }) =>
        !value.name || !value.url || !value.secret
          ? "Name, URL, and signing secret required"
          : undefined,
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: () => toastError("Name, URL, and signing secret required"),
    onSubmit: async ({ value }) => {
      if (!selected) return;
      const filters: WebhookFilter = { events: value.events };
      try {
        const created = await createMutation.mutateAsync({
          name: value.name,
          url: value.url,
          template: selected.template,
          secret: value.secret || undefined,
          enabled: true,
          filters,
        });
        router.push(`/dashboard/settings/webhooks/${created.id}`);
      } catch {
        // mutation toasts on error
      }
    },
  });
  // The preview step + the step-advance gate read live form values.
  const name = useStore(form.store, (s) => s.values.name);
  const url = useStore(form.store, (s) => s.values.url);
  const events = useStore(form.store, (s) => s.values.events);

  const title =
    step === "pick"
      ? "Choose a webhook template"
      : step === "configure"
        ? `Configure ${selected?.label} webhook`
        : "Preview & create";

  return (
    <PageShell>
      <Link
        href="/dashboard/settings/webhooks"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to webhooks
      </Link>
      <PageHeader eyebrow="Webhooks · New" title={title} />

      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        {(["pick", "configure", "preview"] as Step[]).map((s, idx) => (
          <div key={s} className="flex items-center gap-2">
            <span
              className={cn(
                "inline-flex h-6 w-6 items-center justify-center rounded-full border text-2xs font-medium",
                step === s
                  ? "border-foreground text-foreground"
                  : "border-border text-muted-foreground",
              )}
            >
              {idx + 1}
            </span>
            <span className={cn(step === s && "text-foreground")}>{s}</span>
            {idx < 2 && <span className="text-muted-foreground/40">/</span>}
          </div>
        ))}
      </div>

      {step === "pick" && (
        <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
          {TEMPLATES.map((t) => {
            const Icon = t.icon;
            return (
              <button
                key={t.template}
                type="button"
                onClick={() => {
                  setSelected(t);
                  setStep("configure");
                }}
                className="flex flex-col gap-2 p-4 rounded-lg border border-border bg-card text-left hover:bg-card/80 hover:border-foreground/20 transition-colors"
              >
                <div className="flex items-center gap-2">
                  <div className="flex-shrink-0 w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
                    <Icon className="h-4 w-4 text-foreground" />
                  </div>
                  <p className="text-sm font-medium text-foreground">
                    {t.label}
                  </p>
                </div>
                <p className="text-xs text-muted-foreground line-clamp-3">
                  {t.description}
                </p>
              </button>
            );
          })}
        </div>
      )}

      {step === "configure" && selected && (
        <div className="rounded-xl border border-border bg-card p-6 space-y-4">
          <div className="space-y-1.5">
            <label
              className="text-sm font-medium text-foreground"
              htmlFor="field-09b1054d-219"
            >
              Name
            </label>
            <form.Field name="name">
              {(field) => (
                <Input
                  id="field-09b1054d-219"
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="On-call alerts"
                  data-initial-focus
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <label
              className="text-sm font-medium text-foreground"
              htmlFor="field-09b1054d-235"
            >
              URL
            </label>
            <form.Field name="url">
              {(field) => (
                <Input
                  id="field-09b1054d-235"
                  type="url"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder={selected.urlPlaceholder}
                  className="font-mono"
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <label
              className="text-sm font-medium text-foreground"
              htmlFor="field-09b1054d-251"
            >
              Signing secret{" "}
              <span className="text-muted-foreground font-normal">
                (optional)
              </span>
            </label>
            <form.Field name="secret">
              {(field) => (
                <Input
                  id="field-09b1054d-251"
                  type="password"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="HMAC secret used to sign the X-Astronomer-Signature header"
                />
              )}
            </form.Field>
          </div>

          <div className="space-y-1.5">
            <span
              id="new-webhook-events-label"
              className="text-sm font-medium text-foreground"
            >
              Events
            </span>
            <form.Field name="events">
              {(field) => (
                <div
                  role="group"
                  aria-labelledby="new-webhook-events-label"
                  className="flex flex-wrap gap-1.5"
                >
                  {AVAILABLE_EVENTS.map((ev) => {
                    const checked = field.state.value.includes(ev);
                    return (
                      <button
                        key={ev}
                        type="button"
                        onClick={() =>
                          field.handleChange(
                            field.state.value.includes(ev)
                              ? field.state.value.filter((e) => e !== ev)
                              : [...field.state.value, ev],
                          )
                        }
                        className={cn(
                          "text-2xs px-2 py-1 rounded-full border font-mono transition-colors",
                          checked
                            ? "border-foreground bg-foreground text-background"
                            : "border-border text-muted-foreground hover:text-foreground hover:border-foreground/50",
                        )}
                      >
                        {ev}
                      </button>
                    );
                  })}
                </div>
              )}
            </form.Field>
            <p className="text-xs text-muted-foreground">
              {events.length === 0
                ? "Empty = subscribe to every event"
                : `${events.length} event(s) selected`}
            </p>
          </div>

          <div className="flex justify-end gap-2 pt-2 border-t border-border">
            <ActionButton onClick={() => setStep("pick")}>Back</ActionButton>
            <ActionButton
              intent="primary"
              onClick={() => setStep("preview")}
              disabled={!name || !url}
              icon={<Settings2 className="h-3.5 w-3.5" />}
            >
              Preview
            </ActionButton>
          </div>
        </div>
      )}

      {step === "preview" && selected && (
        <div className="rounded-xl border border-border bg-card p-6 space-y-4">
          <div>
            <h2 className="text-base font-semibold text-foreground">
              Sample payload
            </h2>
            <p className="text-xs text-muted-foreground mt-0.5">
              This is the shape the receiver will see for the first matching
              event. Headers include{" "}
              <span className="font-mono">X-Astronomer-Event</span> and (if a
              secret is set){" "}
              <span className="font-mono">X-Astronomer-Signature</span>.
            </p>
          </div>
          <CodeBlock
            code={JSON.stringify(selected.samplePayload, null, 2)}
            title={`${selected.label} payload`}
          />
          <div className="rounded-lg border border-border bg-background p-3 space-y-1 text-xs">
            <p>
              <span className="text-muted-foreground">Name: </span>
              <span className="text-foreground font-medium">{name}</span>
            </p>
            <p className="truncate">
              <span className="text-muted-foreground">URL: </span>
              <span className="text-foreground font-mono">{url}</span>
            </p>
            <p>
              <span className="text-muted-foreground">Events: </span>
              <span className="text-foreground font-mono">
                {events.length ? events.join(", ") : "(all)"}
              </span>
            </p>
          </div>
          <div className="flex justify-end gap-2 pt-2 border-t border-border">
            <ActionButton onClick={() => setStep("configure")}>
              Back
            </ActionButton>
            <ActionButton
              intent="primary"
              onClick={() => void form.handleSubmit()}
              loading={createMutation.isPending}
            >
              Create webhook
            </ActionButton>
          </div>
        </div>
      )}
    </PageShell>
  );
}

function NewWebhookPage() {
  return (
    <SettingsAuthGate>
      <NewWebhookWizard />
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/webhooks/new/")({
  component: NewWebhookPage,
});
