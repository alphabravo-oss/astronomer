import { useState } from "react";
import { ShieldAlert } from "lucide-react";
import { useAppForm, useStore } from "@/lib/form";
import { ActionButton } from "@/components/ui/action-button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { ModalShell } from "@/components/ui/modal-shell";
import type { SIEMForwarder } from "@/types";
import { forwarderDefaults, forwarderRequest } from "./-forwarder-form";
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

  const [serverError, setServerError] = useState<string>();
  const form = useAppForm({
    defaultValues: forwarderDefaults(forwarder),
    onSubmit: async ({ value }) => {
      const body = forwarderRequest(value, forwarder);

      try {
        if (forwarder) {
          await update.mutateAsync({ id: forwarder.id, body });
        } else {
          await create.mutateAsync(body);
        }
        setServerError(undefined);
        onClose();
      } catch (error) {
        setServerError(
          error instanceof Error ? error.message : "Unable to save",
        );
      }
    },
  });

  const name = useStore(form.store, (s) => s.values.name);
  const endpoint = useStore(form.store, (s) => s.values.endpoint);
  const tlsSkipVerify = useStore(form.store, (s) => s.values.tlsSkipVerify);

  const isPending = create.isPending || update.isPending;
  return (
    <ModalShell
      onSubmit={(event) => {
        event.preventDefault();
        if (!form.store.state.isSubmitting) void form.handleSubmit();
      }}
      title={isEdit ? "Edit SIEM Forwarder" : "Add SIEM Forwarder"}
      onClose={onClose}
      size="md"
      footerClassName="flex items-center justify-end gap-2"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            type="submit"
            disabled={isPending || !name || !endpoint}
            loading={isPending}
          >
            {isEdit ? "Save Changes" : "Create Forwarder"}
          </ActionButton>
        </>
      }
    >
      <form.AppForm>
        <form.FormErrorSummary serverError={serverError} />
      </form.AppForm>
      <div className="space-y-1.5">
        <label
          className="text-sm font-medium text-foreground"
          htmlFor="field-97739a4a-335"
        >
          Name
        </label>
        <form.Field
          name="name"
          validators={{
            onSubmit: ({ value }) =>
              !value.trim() ? "Name is required" : undefined,
          }}
        >
          {(field) => (
            <Input
              name={field.name}
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
                name={field.name}
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
                name={field.name}
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
        <form.Field
          name="endpoint"
          validators={{
            onSubmit: ({ value }) =>
              !value.trim() ? "Endpoint is required" : undefined,
          }}
        >
          {(field) => (
            <Input
              name={field.name}
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
        <label
          htmlFor="siem-auth"
          className="text-sm font-medium text-foreground"
        >
          Auth{" "}
          {isEdit && (
            <span className="text-2xs text-muted-foreground font-normal">
              (leave blank to keep existing)
            </span>
          )}
        </label>
        <p id="siem-auth-help" className="text-xs text-muted-foreground">
          Leave blank when editing to retain stored credentials.
        </p>
        <form.Field name="auth">
          {(field) => (
            <Input
              name={field.name}
              type="password"
              id="siem-auth"
              aria-describedby="siem-auth-help"
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
              name={field.name}
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
                name={field.name}
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
                name={field.name}
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
                name={field.name}
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
              name={field.name}
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
                name={field.name}
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
                name={field.name}
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
