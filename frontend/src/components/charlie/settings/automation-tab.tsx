import { LoadingPanel } from "@/components/charlie/loading-panel";
import { useState } from "react";
import { useDraft } from "@/lib/hooks/use-draft";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Save } from "lucide-react";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { automationValidationIssues } from "@/components/charlie/admin-utils";
import {
  deleteCharlieAutomationRule,
  getCharlieAutomation,
  listCharlieTriggerEvents,
  retryCharlieTriggerEvent,
  updateCharlieAutomation,
  updateCharlieActionPolicy,
  type CharlieActionPolicyInput,
  type CharlieAutomationView,
  type CharlieTriggerRule,
  type CharlieTriggerEvent,
} from "@/lib/api/charlie-admin";
import { Section, Unavailable, button } from "./shared";
import { ActionButton } from "@/components/ui/action-button";
import { AutomationActionPolicies } from "./automation-action-policies";
import { AutomationDeadLetters } from "./automation-dead-letters";
import { newAutomationRule } from "./automation-model";
import { AutomationRuleSection } from "./automation-rule-section";

export function AutomationTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAutomation,
    queryFn: ({ signal }) => getCharlieAutomation(signal),
    retry: false,
  });
  const deadLetters = useQuery({
    queryKey: queryKeys.charlie.adminTriggerEvents("dead"),
    queryFn: () => listCharlieTriggerEvents("dead", 0, 20),
    retry: false,
  });
  const [draft, setDraft] = useDraft(q.data);
  const [deleteRule, setDeleteRule] = useState<CharlieTriggerRule>();
  const [retryEvent, setRetryEvent] = useState<CharlieTriggerEvent>();
  const [policyError, setPolicyError] = useState("");
  const save = useMutation({
    mutationFn: (input: CharlieAutomationView) =>
      updateCharlieAutomation(input),
    onSuccess: (v) => {
      qc.setQueryData(queryKeys.charlie.adminAutomation, v);
      setDraft(structuredClone(v));
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminConnection,
      });
      toastSuccess("Charlie automation configuration saved");
    },
    onError: (e) => toastApiError("Automation save failed", e),
  });
  const savePolicy = useMutation({
    mutationFn: (input: CharlieActionPolicyInput) =>
      updateCharlieActionPolicy(input),
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
    mutationFn: (id: string) => deleteCharlieAutomationRule(id),
    onSuccess: () => {
      setDeleteRule(undefined);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminAutomation,
      });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminConnection,
      });
      toastSuccess("Charlie automation rule deleted");
    },
    onError: (e) => toastApiError("Automation rule deletion failed", e),
  });
  const retry = useMutation({
    mutationFn: (id: string) => retryCharlieTriggerEvent(id),
    onSuccess: () => {
      setRetryEvent(undefined);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.adminTriggerEvents("dead"),
      });
      toastSuccess("Charlie trigger retry queued");
    },
    onError: (e) => toastApiError("Trigger retry failed", e),
  });
  if (q.isLoading) return <LoadingPanel title="Loading automation" />;
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
      <AutomationActionPolicies
        policies={draft.actionPolicies}
        savedPolicies={q.data?.actionPolicies}
        policyError={policyError}
        saving={savePolicy.isPending}
        updatePolicy={updatePolicy}
        onSave={(policy) => savePolicy.mutate(policy)}
      />
      {draft.rules.map((r, i) => (
        <AutomationRuleSection
          key={r.id || `new-${i}`}
          rule={r}
          index={i}
          ruleCount={draft.rules.length}
          update={update}
          onDelete={() => {
            if (r.id) setDeleteRule(r);
            else
              setDraft(
                (value) =>
                  value && {
                    ...value,
                    rules: value.rules.filter((_, index) => index !== i),
                  },
              );
          }}
        />
      ))}
      {issues.length > 0 && (
        <div
          role="alert"
          className="rounded-lg border border-status-error/30 p-4"
        >
          <p className="text-sm font-medium">Automation needs attention</p>
          <ul className="mt-2 list-disc pl-5 text-xs text-muted-foreground">
            {issues.map((issue) => (
              <li key={issue}>{issue}</li>
            ))}
          </ul>
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <ActionButton
          intent="bare"
          size="none"
          onClick={() =>
            setDraft({ ...draft, rules: [...draft.rules, newAutomationRule()] })
          }
          className={button}
        >
          <Plus className="h-4 w-4" />
          Add trigger rule
        </ActionButton>
        <ActionButton
          intent="primary"
          disabled={save.isPending || issues.length > 0}
          onClick={() => save.mutate(draft)}
        >
          <Save className="h-4 w-4" />
          Save automation
        </ActionButton>
      </div>
      <AutomationDeadLetters
        deadLetters={deadLetters}
        onRetry={setRetryEvent}
      />
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
