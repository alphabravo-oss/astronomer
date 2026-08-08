import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Save } from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import {
  getCharlieAlertPolicy,
  updateCharlieAlertPolicy,
  type CharlieAlertPolicy,
} from "@/lib/api/charlie-admin";
import { Field, NumberField, Section, Unavailable, button, field, primary } from "./shared";

export function AlertsTab() {
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: queryKeys.charlie.adminAlertPolicy,
    queryFn: getCharlieAlertPolicy,
    retry: false,
  });
  const [draft, setDraft] = useState<CharlieAlertPolicy>();
  useEffect(() => {
    if (q.data) setDraft(structuredClone(q.data));
  }, [q.data]);
  const save = useMutation({
    mutationFn: updateCharlieAlertPolicy,
    onSuccess: (value) => {
      qc.setQueryData(queryKeys.charlie.adminAlertPolicy, value);
      setDraft(structuredClone(value));
      toastSuccess("Charlie alert policy saved");
    },
    onError: (error) => toastApiError("Alert policy save failed", error),
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie alert policy"
      />
    );
  if (q.isError || !draft)
    return <Unavailable name="Alert policy" retry={() => void q.refetch()} />;
  const changed = JSON.stringify(draft) !== JSON.stringify(q.data);
  const valid =
    draft.dedupeWindowSeconds >= 60 &&
    draft.dedupeWindowSeconds <= 604800 &&
    (draft.escalationAfterSeconds === 0 ||
      (draft.escalationAfterSeconds >= 300 &&
        draft.escalationAfterSeconds <= 604800)) &&
    /^([01]\d|2[0-3]):[0-5]\d$/.test(draft.quietHoursStart) &&
    /^([01]\d|2[0-3]):[0-5]\d$/.test(draft.quietHoursEnd) &&
    !!draft.quietHoursTimezone.trim();
  const toggleChannel = (id: string, selected: boolean) =>
    setDraft({
      ...draft,
      channelIds: selected
        ? [...draft.channelIds, id]
        : draft.channelIds.filter((candidate) => candidate !== id),
    });
  return (
    <div className="space-y-4">
      <Section
        title="Actionable finding alerts"
        description="Astronomer owns thresholds, recipients, routing, quiet hours, deduplication, and escalation. These settings and channel credentials are never sent to Charlie."
      >
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
          />
          Enable external alerts for Charlie findings
        </label>
        <p className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
          Findings always remain durable and visible in Astronomer. An alert is
          informational only: it cannot approve, authorize, or dispatch work.
        </p>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <label className="space-y-1">
            <span className="text-xs text-muted-foreground">Minimum severity</span>
            <select
              className={field}
              value={draft.minimumSeverity}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  minimumSeverity: event.target.value as CharlieAlertPolicy["minimumSeverity"],
                })
              }
            >
              {(["info", "low", "medium", "high", "critical"] as const).map((severity) => (
                <option key={severity} value={severity}>{severity}</option>
              ))}
            </select>
          </label>
          <NumberField
            label="Dedupe window (seconds)"
            value={draft.dedupeWindowSeconds}
            min={60}
            max={604800}
            set={(value) => setDraft({ ...draft, dedupeWindowSeconds: value })}
          />
          <NumberField
            label="Escalate after (seconds, 0 disables)"
            value={draft.escalationAfterSeconds}
            min={0}
            max={604800}
            set={(value) => setDraft({ ...draft, escalationAfterSeconds: value })}
          />
          <Field
            label="Quiet-hours timezone"
            value={draft.quietHoursTimezone}
            set={(value) => setDraft({ ...draft, quietHoursTimezone: value })}
          />
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={draft.quietHoursEnabled}
            onChange={(event) => setDraft({ ...draft, quietHoursEnabled: event.target.checked })}
          />
          Delay delivery during quiet hours
        </label>
        {draft.quietHoursEnabled && (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="Quiet hours start" type="time" value={draft.quietHoursStart} set={(value) => setDraft({ ...draft, quietHoursStart: value })} />
            <Field label="Quiet hours end" type="time" value={draft.quietHoursEnd} set={(value) => setDraft({ ...draft, quietHoursEnd: value })} />
          </div>
        )}
      </Section>
      <Section
        title="Recipients and channels"
        description="Select locally configured Astronomer notification channels. Destination values remain redacted and are resolved only by the delivery worker."
      >
        {draft.availableChannels.length ? (
          <div className="grid gap-2 md:grid-cols-2">
            {draft.availableChannels.map((channel) => (
              <label key={channel.id} className="flex items-center gap-3 rounded-lg border p-3 text-sm">
                <input
                  type="checkbox"
                  aria-label={`Route Charlie alerts to ${channel.name}`}
                  checked={draft.channelIds.includes(channel.id)}
                  onChange={(event) => toggleChannel(channel.id, event.target.checked)}
                />
                <span className="flex-1">
                  <span className="block font-medium">{channel.name}</span>
                  <span className="block text-xs text-muted-foreground">{channel.type} · configured recipient</span>
                </span>
              </label>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            No supported notification channels are enabled. Configure Slack,
            PagerDuty, Teams, or a webhook under Alerting first.
          </p>
        )}
        <div className="flex items-center justify-between gap-3">
          <p className="text-xs text-muted-foreground">Policy revision {draft.revision || "not saved"}</p>
          <button
            type="button"
            className={primary}
            disabled={!changed || !valid || save.isPending}
            onClick={() => save.mutate(draft)}
          >
            <Save className="h-4 w-4" />
            Save alert policy
          </button>
        </div>
      </Section>
    </div>
  );
}
