import { ShieldAlert } from "lucide-react";
import { useAppForm, useStore } from "@/lib/form";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { ModalShell } from "@/components/ui/modal-shell";
import type { SIEMForwarder } from "@/types";
import {
  SIEM_AUTH_SENTINEL,
  type SIEMForwarderWriteRequest,
} from "@/lib/api/siem-forwarders";
import { useCreateSIEMForwarder, useUpdateSIEMForwarder } from "./-hooks";
import { FORMATS, TRANSPORTS } from "./-options";

export function SIEMForwarderModal({
  forwarder,
  onClose,
}: {
  forwarder: SIEMForwarder | null;
  onClose: () => void;
}) {
  const create = useCreateSIEMForwarder();
  const update = useUpdateSIEMForwarder();
  const isEdit = !!forwarder;

  const form = useAppForm({
    defaultValues: {
      name: forwarder?.name ?? "",
      transport: forwarder?.transport ?? "syslog_tls",
      endpoint: forwarder?.endpoint ?? "",
      // On edit the real auth is never sent to the client; leave blank and only
      // submit a new value if the operator types one.
      auth: "",
      eventFilters: (forwarder?.eventFilters ?? []).join(", "),
      format: forwarder?.format ?? "",
      tlsSkipVerify: forwarder?.tlsSkipVerify ?? false,
      caCertPem: "",
      batchSize: forwarder?.batchSize ?? 100,
      flushIntervalMs: forwarder?.flushIntervalMs ?? 5000,
      timeoutSeconds: forwarder?.timeoutSeconds ?? 10,
      enabled: forwarder?.enabled ?? true,
    },
    onSubmit: async ({ value }) => {
      const filters = value.eventFilters
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      const body: SIEMForwarderWriteRequest = {
        name: value.name,
        transport: value.transport,
        endpoint: value.endpoint,
        event_filters: filters,
        format: value.format,
        tls_skip_verify: value.tlsSkipVerify,
        batch_size: value.batchSize,
        flush_interval_ms: value.flushIntervalMs,
        timeout_seconds: value.timeoutSeconds,
        enabled: value.enabled,
      };
      // Only send auth when the operator supplied a new value; on edit an empty
      // field means "keep existing" (we echo the sentinel so a blank PUT doesn't
      // wipe the stored blob).
      if (value.auth.trim()) {
        body.auth = value.auth;
      } else if (isEdit && forwarder?.authConfigured) {
        body.auth = SIEM_AUTH_SENTINEL;
      }
      if (value.caCertPem.trim()) {
        body.ca_cert_pem = value.caCertPem;
      }

      try {
        if (forwarder) {
          await update.mutateAsync({ id: forwarder.id, body });
        } else {
          await create.mutateAsync(body);
        }
        onClose();
      } catch {
        /* mutation toasts on error */
      }
    },
  });

  // Old disabled gate (`!form.name || !form.endpoint`), recomputed from form
  // state — the save button below keeps the identical condition.
  const name = useStore(form.store, (s) => s.values.name);
  const endpoint = useStore(form.store, (s) => s.values.endpoint);
  const tlsSkipVerify = useStore(form.store, (s) => s.values.tlsSkipVerify);

  const isPending = create.isPending || update.isPending;
  return (
    <ModalShell
      title={isEdit ? "Edit SIEM Forwarder" : "Add SIEM Forwarder"}
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void form.handleSubmit()}
            disabled={isPending || !name || !endpoint}
            loading={isPending}
          >
            {isEdit ? "Save Changes" : "Create Forwarder"}
          </ActionButton>
        </>
      }
    >
      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-97739a4a-335"
        >
          Name
        </label>
        <form.Field name="name">
          {(field) => (
            <Input
              id="field-97739a4a-335"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="corp-splunk"
            />
          )}
        </form.Field>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-97739a4a-351"
          >
            Transport
          </label>
          <form.Field name="transport">
            {(field) => (
              <Select
                id="field-97739a4a-351"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
              >
                {TRANSPORTS.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </Select>
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-97739a4a-369"
          >
            Format
          </label>
          <form.Field name="format">
            {(field) => (
              <Select
                id="field-97739a4a-369"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
              >
                {FORMATS.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </Select>
            )}
          </form.Field>
        </div>
      </div>

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-97739a4a-389"
        >
          Endpoint
        </label>
        <form.Field name="endpoint">
          {(field) => (
            <Input
              id="field-97739a4a-389"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="siem.corp.example.com:6514"
              className="font-mono"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">
          Auth{" "}
          {isEdit && (
            <span className="text-2xs text-muted-foreground font-normal">
              (leave blank to keep existing)
            </span>
          )}
        </label>
        <form.Field name="auth">
          {(field) => (
            <Input
              type="password"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder={
                isEdit && forwarder?.authConfigured
                  ? "•••••••• (configured)"
                  : "HEC token / bearer / password"
              }
              className="font-mono"
              autoComplete="new-password"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-97739a4a-424"
        >
          Event filters{" "}
          <span className="text-2xs text-muted-foreground font-normal">
            (comma-separated; blank = all)
          </span>
        </label>
        <form.Field name="eventFilters">
          {(field) => (
            <Input
              id="field-97739a4a-424"
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="auth.login.failed, admin.*"
              className="font-mono"
            />
          )}
        </form.Field>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-97739a4a-443"
          >
            Batch size
          </label>
          <form.Field name="batchSize">
            {(field) => (
              <Input
                id="field-97739a4a-443"
                type="number"
                min={1}
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(parseInt(e.target.value, 10) || 0)
                }
                onBlur={field.handleBlur}
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-97739a4a-457"
          >
            Flush (ms)
          </label>
          <form.Field name="flushIntervalMs">
            {(field) => (
              <Input
                id="field-97739a4a-457"
                type="number"
                min={0}
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(parseInt(e.target.value, 10) || 0)
                }
                onBlur={field.handleBlur}
              />
            )}
          </form.Field>
        </div>
        <div className="space-y-1.5">
          <label
            className="text-sm font-medium text-foreground"
            htmlFor="field-97739a4a-471"
          >
            Timeout (s)
          </label>
          <form.Field name="timeoutSeconds">
            {(field) => (
              <Input
                id="field-97739a4a-471"
                type="number"
                min={1}
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(parseInt(e.target.value, 10) || 0)
                }
                onBlur={field.handleBlur}
              />
            )}
          </form.Field>
        </div>
      </div>

      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-97739a4a-487"
        >
          CA certificate (PEM){" "}
          <span className="text-2xs text-muted-foreground font-normal">
            (optional; leave blank to keep)
          </span>
        </label>
        <form.Field name="caCertPem">
          {(field) => (
            <Textarea
              id="field-97739a4a-487"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="-----BEGIN CERTIFICATE-----"
              rows={3}
              className="min-h-0 resize-none"
            />
          )}
        </form.Field>
      </div>

      <div className="flex items-center justify-between gap-4">
        <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
          <form.Field name="enabled">
            {(field) => (
              <Input
                type="checkbox"
                checked={field.state.value}
                onChange={(e) => field.handleChange(e.target.checked)}
                onBlur={field.handleBlur}
                className="h-4 w-4 rounded-sm border-border"
              />
            )}
          </form.Field>
          Enabled
        </label>
        <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
          <form.Field name="tlsSkipVerify">
            {(field) => (
              <Input
                type="checkbox"
                checked={field.state.value}
                onChange={(e) => field.handleChange(e.target.checked)}
                onBlur={field.handleBlur}
                className="h-4 w-4 rounded-sm border-border"
              />
            )}
          </form.Field>
          <span className="inline-flex items-center gap-1">
            {tlsSkipVerify && (
              <ShieldAlert className="h-3.5 w-3.5 text-status-warning" />
            )}
            Skip TLS verify
          </span>
        </label>
      </div>
    </ModalShell>
  );
}
