import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { automationValidationIssues } from "@/components/charlie/admin-utils";
import { formatRelativeTime } from "@/lib/utils";
import {
  deleteCharlieAutomationRule,
  getCharlieAutomation,
  listCharlieTriggerEvents,
  retryCharlieTriggerEvent,
  updateCharlieAutomation,
  updateCharlieActionPolicy,
  type CharlieAutomationView,
  type CharlieTriggerRule,
  type CharlieTriggerEvent,
} from "@/lib/api/charlie-admin";
import { Field, Meta, NumberField, Section, Unavailable, button, primary } from "./shared";

const newAutomationRule = (): CharlieTriggerRule => ({
  id: "",
  name: "",
  enabled: false,
  sourceType: "event",
  severities: ["high", "critical"],
  scopes: [],
  cooldownSeconds: 1800,
  gracePeriodSeconds: 300,
  flapWindowSeconds: 900,
  flapCount: 3,
  fleetThresholdPercent: 25,
  minimumAgentVersion: "",
  suppressed: false,
  maximumAttempts: 3,
  deadLetterEnabled: true,
  serviceIdentity: "system:charlie-automation",
  modeCeiling: "read_only",
});

export function AutomationTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAutomation,
    queryFn: getCharlieAutomation,
    retry: false,
  });
  const deadLetters = useQuery({
    queryKey: queryKeys.charlie.adminTriggerEvents("dead"),
    queryFn: () => listCharlieTriggerEvents("dead", 0, 20),
    retry: false,
  });
  const [draft, setDraft] = useState<CharlieAutomationView>();
  const [deleteRule, setDeleteRule] = useState<CharlieTriggerRule>();
  const [retryEvent, setRetryEvent] = useState<CharlieTriggerEvent>();
  const [policyError, setPolicyError] = useState("");
  useEffect(() => {
    if (q.data) setDraft(structuredClone(q.data));
  }, [q.data]);
  const save = useMutation({
    mutationFn: updateCharlieAutomation,
    onSuccess: (v) => {
      qc.setQueryData(queryKeys.charlie.adminAutomation, v);
      setDraft(structuredClone(v));
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess("Charlie automation configuration saved");
    },
    onError: (e) => toastApiError("Automation save failed", e),
  });
  const savePolicy = useMutation({
    mutationFn: updateCharlieActionPolicy,
    onMutate: () => setPolicyError(""),
    onSuccess: (policy) => {
      const replacePolicy = (value: CharlieAutomationView | undefined) =>
        value && {
          ...value,
          actionPolicies: value.actionPolicies.map((candidate) =>
            candidate.capability === policy.capability ? policy : candidate,
          ),
        };
      setDraft((value) => replacePolicy(value));
      qc.setQueryData<CharlieAutomationView>(
        queryKeys.charlie.adminAutomation,
        replacePolicy,
      );
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      toastSuccess(`Action policy saved: ${policy.capability}`);
    },
    onError: (error) => {
      const message =
        error instanceof Error
          ? error.message
          : "Astronomer could not confirm the action-policy update.";
      setPolicyError(message);
      toastApiError("Action-policy update failed", error);
    },
  });
  const remove = useMutation({
    mutationFn: deleteCharlieAutomationRule,
    onSuccess: () => {
      setDeleteRule(undefined);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAutomation });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess("Charlie automation rule deleted");
    },
    onError: (e) => toastApiError("Automation rule deletion failed", e),
  });
  const retry = useMutation({
    mutationFn: retryCharlieTriggerEvent,
    onSuccess: () => {
      setRetryEvent(undefined);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminTriggerEvents("dead"),
      });
      toastSuccess("Charlie trigger retry queued");
    },
    onError: (e) => toastApiError("Trigger retry failed", e),
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading automation"
      />
    );
  if (q.isError || !draft)
    return (
      <Unavailable
        name="Automation configuration"
        retry={() => void q.refetch()}
      />
    );
  const update = (
    index: number,
    patch: Partial<CharlieAutomationView["rules"][number]>,
  ) =>
    setDraft(
      (v) =>
        v && {
          ...v,
          rules: v.rules.map((r, i) => (i === index ? { ...r, ...patch } : r)),
        },
    );
  const updatePolicy = (
    index: number,
    patch: Partial<CharlieAutomationView["actionPolicies"][number]>,
  ) =>
    setDraft((value) =>
      value
        ? {
            ...value,
            actionPolicies: value.actionPolicies.map((policy, candidate) =>
              candidate === index ? { ...policy, ...patch } : policy,
            ),
          }
        : value,
    );
  const issues = automationValidationIssues(draft);
  return (
    <div className="space-y-4">
      <Section
        title="Automation identity"
        description="Astronomer evaluates product triggers and delegates only opaque, scoped authority. Charlie does not define this role engine."
      >
        <label className="flex gap-2 text-sm">
          <input
            type="checkbox"
            checked={draft.serviceIdentityEnabled}
            onChange={(e) =>
              setDraft({ ...draft, serviceIdentityEnabled: e.target.checked })
            }
          />
          Enable Charlie automation service identity
        </label>
        <p className="text-xs text-muted-foreground">
          Defaults revision {draft.defaultsRevision}
        </p>
      </Section>
      <Section
        title="Automatic-action policies"
        description="The exact intersection of Charlie's central capability allowlist and Astronomer's local budgets and safety controls. This view never includes action arguments or authority references."
      >
        {draft.actionPolicies.length ? (
          <div className="space-y-3">
            {draft.actionPolicies.map((policy, policyIndex) => {
              const mayEnable =
                policy.centralState === "verified" &&
                policy.centralAllowlisted &&
                policy.autoEligible;
              const original = q.data?.actionPolicies.find(
                (candidate) => candidate.capability === policy.capability,
              );
              const changed =
                !!original &&
                ([
                  "enabled",
                  "maxActionsPerIncident",
                  "maxActionsPerWindow",
                  "budgetWindowSeconds",
                  "cooldownSeconds",
                ] as const).some((field) => original[field] !== policy[field]);
              const valuesValid =
                policy.maxActionsPerIncident >= 1 &&
                policy.maxActionsPerIncident <= 100 &&
                policy.maxActionsPerWindow >= 1 &&
                policy.maxActionsPerWindow <= 100 &&
                policy.budgetWindowSeconds >= 60 &&
                policy.budgetWindowSeconds <= 86400 &&
                policy.cooldownSeconds >= 30 &&
                policy.cooldownSeconds <= 604800;
              return (
              <article key={policy.capability} className="rounded-lg border p-4">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <h3 className="font-mono text-sm font-medium">{policy.capability}</h3>
                    <p className="mt-1 text-xs text-muted-foreground">{policy.effect}</p>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <StatusBadge status={policy.enabled ? "healthy" : "disabled"} label={policy.enabled ? "Enabled" : "Disabled"} />
                    <StatusBadge status={policy.centralState === "verified" ? "healthy" : "unavailable"} label={`Central: ${policy.centralState}`} />
                    <StatusBadge status={policy.circuitState} label={`Circuit: ${policy.circuitState}`} />
                  </div>
                </div>
                <dl className="mt-3 grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
                  <Meta label="Risk" value={policy.risk} />
                  <Meta label="Auto eligible" value={policy.autoEligible ? "Yes" : "No"} />
                  <Meta label="Central allowlisted" value={policy.centralAllowlisted ? "Yes" : "No"} />
                  <Meta label="Revision" value={policy.revision} />
                </dl>
                <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <NumberField label="Max actions per incident" value={policy.maxActionsPerIncident} min={1} max={100} set={(value) => updatePolicy(policyIndex, { maxActionsPerIncident: value })} />
                  <NumberField label="Max actions per window" value={policy.maxActionsPerWindow} min={1} max={100} set={(value) => updatePolicy(policyIndex, { maxActionsPerWindow: value })} />
                  <NumberField label="Budget window seconds" value={policy.budgetWindowSeconds} min={60} max={86400} set={(value) => updatePolicy(policyIndex, { budgetWindowSeconds: value })} />
                  <NumberField label="Cooldown seconds" value={policy.cooldownSeconds} min={30} max={604800} set={(value) => updatePolicy(policyIndex, { cooldownSeconds: value })} />
                </div>
                <label className="mt-3 flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    aria-label={`Enable ${policy.capability}`}
                    checked={policy.enabled}
                    disabled={!policy.enabled && !mayEnable}
                    onChange={(event) => updatePolicy(policyIndex, { enabled: event.target.checked })}
                  />
                  Enable bounded automatic action
                </label>
                {!mayEnable && !policy.enabled && (
                  <p className="mt-2 text-xs text-status-warning">
                    Enabling is unavailable until Charlie central is verified,
                    centrally allowlists this capability, and marks it auto eligible.
                  </p>
                )}
                <p className="mt-3 text-xs"><span className="font-medium">Scope:</span> {policy.scopeSummary}</p>
                <p className="mt-2 text-xs"><span className="font-medium">Verification:</span> {policy.verification}</p>
                <div className="mt-2 text-xs">
                  <span className="font-medium">Preconditions:</span>{" "}
                  {policy.preconditions.length ? policy.preconditions.join("; ") : "None published"}
                </div>
                <button
                  type="button"
                  className={`${primary} mt-3`}
                  disabled={!changed || !valuesValid || savePolicy.isPending}
                  onClick={() => savePolicy.mutate(policy)}
                >
                  <Save className="h-4 w-4" />
                  Save action policy
                </button>
              </article>
              );
            })}
            {policyError && (
              <p role="alert" className="rounded-lg border border-status-error/30 p-3 text-sm text-status-error">
                {policyError}
              </p>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            No bounded automatic-action policy was published. Astronomer will
            not imply automatic authority.
          </p>
        )}
      </Section>
      {draft.rules.map((r, i) => (
        <Section
          key={r.id || `new-${i}`}
          title={r.name || "New trigger rule"}
          description={`${r.sourceType} · service identity ${r.serviceIdentity}`}
        >
          <label className="flex gap-2 text-sm">
            <input
              type="checkbox"
              checked={r.enabled}
              onChange={(e) => update(i, { enabled: e.target.checked })}
            />
            Enabled
          </label>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Field
              label="Rule name"
              value={r.name}
              set={(v) => update(i, { name: v })}
            />
            <Field
              label="Source type"
              value={r.sourceType}
              set={(v) => update(i, { sourceType: v })}
            />
            <Field
              label="Severities (comma separated)"
              value={r.severities.join(", ")}
              set={(v) =>
                update(i, {
                  severities: v
                    .split(",")
                    .map((value) => value.trim().toLowerCase())
                    .filter(Boolean),
                })
              }
            />
            <Field
              label="Service identity"
              value={r.serviceIdentity}
              set={(v) => update(i, { serviceIdentity: v })}
            />
            <label className="space-y-1 text-sm">
              <span className="block font-medium">Mode ceiling</span>
              <select
                className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
                value={r.modeCeiling}
                onChange={(event) =>
                  update(i, {
                    modeCeiling: event.target.value as CharlieTriggerRule["modeCeiling"],
                  })
                }
              >
                <option value="read_only">Read only</option>
                <option value="approval">Approval required</option>
                <option value="auto">Autonomous</option>
              </select>
              <span className="block text-xs text-muted-foreground">
                This rule can never exceed the deployment mode. Autonomous also requires an eligible central allowlist and local action policy.
              </span>
            </label>
            <NumberField
              label="Cooldown seconds"
              value={r.cooldownSeconds}
              min={1}
              set={(v) => update(i, { cooldownSeconds: v })}
            />
            <NumberField
              label="Grace period seconds"
              value={r.gracePeriodSeconds}
              min={1}
              set={(v) => update(i, { gracePeriodSeconds: v })}
            />
            <NumberField
              label="Flap window seconds"
              value={r.flapWindowSeconds}
              min={1}
              set={(v) => update(i, { flapWindowSeconds: v })}
            />
            <NumberField
              label="Flap count"
              value={r.flapCount}
              min={1}
              set={(v) => update(i, { flapCount: v })}
            />
            <NumberField
              label="Fleet threshold %"
              value={r.fleetThresholdPercent}
              min={0}
              max={100}
              set={(v) => update(i, { fleetThresholdPercent: v })}
            />
            <NumberField
              label="Maximum attempts"
              value={r.maximumAttempts}
              min={1}
              set={(v) => update(i, { maximumAttempts: v })}
            />
            <Field
              label="Minimum agent version"
              value={r.minimumAgentVersion ?? ""}
              set={(v) => update(i, { minimumAgentVersion: v })}
            />
            <Field
              label="Scopes (comma separated)"
              value={r.scopes.join(", ")}
              set={(v) =>
                update(i, {
                  scopes: v
                    .split(",")
                    .map((x) => x.trim())
                    .filter(Boolean),
                })
              }
            />
          </div>
          <div className="flex flex-wrap gap-4">
            <label className="flex gap-2 text-sm">
              <input
                type="checkbox"
                checked={r.suppressed}
                onChange={(e) => update(i, { suppressed: e.target.checked })}
              />
              Suppressed
            </label>
            <label className="flex gap-2 text-sm">
              <input
                type="checkbox"
                checked={r.deadLetterEnabled}
                onChange={(e) =>
                  update(i, { deadLetterEnabled: e.target.checked })
                }
              />
              Dead letters
            </label>
            <button
              type="button"
              className={`${button} text-status-error`}
              onClick={() => {
                if (r.id) setDeleteRule(r);
                else
                  setDraft((value) =>
                    value && {
                      ...value,
                      rules: value.rules.filter((_, index) => index !== i),
                    },
                  );
              }}
            >
              <Trash2 className="h-4 w-4" />
              Delete rule
            </button>
          </div>
        </Section>
      ))}
      {issues.length > 0 && (
        <div role="alert" className="rounded-lg border border-status-error/30 p-4">
          <p className="text-sm font-medium">Automation needs attention</p>
          <ul className="mt-2 list-disc pl-5 text-xs text-muted-foreground">
            {issues.map((issue) => (
              <li key={issue}>{issue}</li>
            ))}
          </ul>
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={() =>
            setDraft({ ...draft, rules: [...draft.rules, newAutomationRule()] })
          }
          className={button}
        >
          <Plus className="h-4 w-4" />
          Add trigger rule
        </button>
        <button
          type="button"
          disabled={save.isPending || issues.length > 0}
          onClick={() => save.mutate(draft)}
          className={primary}
        >
          <Save className="h-4 w-4" />
          Save automation
        </button>
      </div>
      <Section
        title="Dead-letter events"
        description="Bounded lifecycle metadata for failed trigger work. Charlie task content, fingerprints, sessions, and origin details are never exposed here."
      >
        {deadLetters.isLoading ? (
          <p role="status" className="text-sm text-muted-foreground">
            Loading dead-letter events…
          </p>
        ) : deadLetters.isError ? (
          <Unavailable
            name="Dead-letter events"
            retry={() => void deadLetters.refetch()}
          />
        ) : deadLetters.data?.length ? (
          <div className="overflow-x-auto">
            <Table className="w-full min-w-[760px] text-left text-sm">
              <caption className="sr-only">
                Charlie dead-letter trigger lifecycle metadata
              </caption>
              <TableHeader className="text-xs text-muted-foreground">
                <TableRow>
                  <TableHead scope="col" className="px-2 py-2">Event</TableHead>
                  <TableHead scope="col" className="px-2 py-2">Resource</TableHead>
                  <TableHead scope="col" className="px-2 py-2">State</TableHead>
                  <TableHead scope="col" className="px-2 py-2">Attempts</TableHead>
                  <TableHead scope="col" className="px-2 py-2">Last error</TableHead>
                  <TableHead scope="col" className="px-2 py-2">Dead-lettered</TableHead>
                  <TableHead scope="col" className="px-2 py-2"><span className="sr-only">Actions</span></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody className="divide-y divide-border">
                {deadLetters.data.map((event) => (
                  <TableRow key={event.id}>
                    <TableCell className="px-2 py-2">
                      <span className="block font-medium">{event.eventType}</span>
                      <span className="block max-w-48 truncate font-mono text-xs text-muted-foreground" title={event.id}>
                        {event.id}
                      </span>
                    </TableCell>
                    <TableCell className="px-2 py-2">
                      {event.resourceType} · {event.resourceId}
                    </TableCell>
                    <TableCell className="px-2 py-2"><StatusBadge status={event.state} /></TableCell>
                    <TableCell className="px-2 py-2">{event.attemptCount}</TableCell>
                    <TableCell className="px-2 py-2">{event.lastErrorCode || "—"}</TableCell>
                    <TableCell className="px-2 py-2">
                      {event.deadLetteredAt
                        ? formatRelativeTime(event.deadLetteredAt)
                        : "—"}
                    </TableCell>
                    <TableCell className="px-2 py-2 text-right">
                      <button
                        type="button"
                        className={button}
                        onClick={() => setRetryEvent(event)}
                      >
                        <RefreshCw className="h-4 w-4" />
                        Retry
                      </button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            No dead-letter trigger events.
          </p>
        )}
      </Section>
      <ConfirmDialog
        open={!!deleteRule}
        onClose={() => setDeleteRule(undefined)}
        onConfirm={() => deleteRule && remove.mutate(deleteRule.id)}
        title="Delete Charlie trigger rule"
        description={`Delete ${deleteRule?.name || "this trigger rule"}. Existing audit and dead-letter records are preserved.`}
        confirmText="Delete rule"
        confirmValue="DELETE TRIGGER"
        variant="destructive"
        loading={remove.isPending}
      />
      <ConfirmDialog
        open={!!retryEvent}
        onClose={() => setRetryEvent(undefined)}
        onConfirm={() => retryEvent && retry.mutate(retryEvent.id)}
        title="Retry Charlie trigger event"
        description={`Create one new retry attempt for ${retryEvent?.eventType || "this event"}. The dead source event remains immutable.`}
        confirmText="Queue retry"
        confirmValue="RETRY TRIGGER"
        loading={retry.isPending}
      />
    </div>
  );
}
