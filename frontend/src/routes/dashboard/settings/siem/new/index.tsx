import { createFileRoute } from "@tanstack/react-router";
/**
 * /dashboard/settings/siem/new — add an external SIEM forwarder. Editing an
 * existing forwarder stays a modal on settings/siem (see -page.tsx).
 */
import { useState } from "react";
import { Link as RouterLink, useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ShieldAlert } from "lucide-react";
import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { ActionButton } from "@/components/ui/action-button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { PageHeader, PageShell } from "@/components/ui/page";
import type { SIEMForwarderWriteRequest } from "@/lib/api/siem-forwarders";
import { useCreateSIEMForwarder } from "../-hooks";
import { TRANSPORTS, FORMATS } from "../-page";

function DeliverySettingsFields({
  batchSize,
  setBatchSize,
  flushIntervalMs,
  setFlushIntervalMs,
  timeoutSeconds,
  setTimeoutSeconds,
  caCertPem,
  setCaCertPem,
  enabled,
  setEnabled,
  tlsSkipVerify,
  setTlsSkipVerify,
}: {
  batchSize: number;
  setBatchSize: (v: number) => void;
  flushIntervalMs: number;
  setFlushIntervalMs: (v: number) => void;
  timeoutSeconds: number;
  setTimeoutSeconds: (v: number) => void;
  caCertPem: string;
  setCaCertPem: (v: string) => void;
  enabled: boolean;
  setEnabled: (v: boolean) => void;
  tlsSkipVerify: boolean;
  setTlsSkipVerify: (v: boolean) => void;
}) {
  return (
    <>
      <div className="grid grid-cols-3 gap-3">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground" htmlFor="siem-batch">
            Batch size
          </label>
          <Input
            id="siem-batch"
            type="number"
            min={1}
            value={batchSize}
            onChange={(e) => setBatchSize(parseInt(e.target.value, 10) || 0)}
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground" htmlFor="siem-flush">
            Flush (ms)
          </label>
          <Input
            id="siem-flush"
            type="number"
            min={0}
            value={flushIntervalMs}
            onChange={(e) =>
              setFlushIntervalMs(parseInt(e.target.value, 10) || 0)
            }
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground" htmlFor="siem-timeout">
            Timeout (s)
          </label>
          <Input
            id="siem-timeout"
            type="number"
            min={1}
            value={timeoutSeconds}
            onChange={(e) =>
              setTimeoutSeconds(parseInt(e.target.value, 10) || 0)
            }
          />
        </div>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground" htmlFor="siem-cacert">
          CA certificate (PEM){" "}
          <span className="text-2xs text-muted-foreground font-normal">
            (optional)
          </span>
        </label>
        <Textarea
          id="siem-cacert"
          value={caCertPem}
          onChange={(e) => setCaCertPem(e.target.value)}
          placeholder="-----BEGIN CERTIFICATE-----"
          rows={3}
          className="min-h-0 resize-none"
        />
      </div>

      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2">
          <Switch id="siem-enabled" checked={enabled} onCheckedChange={setEnabled} />
          <label htmlFor="siem-enabled" className="text-sm text-foreground">
            Enabled
          </label>
        </div>
        <div className="flex items-center gap-2">
          <Switch
            id="siem-tls-skip-verify"
            checked={tlsSkipVerify}
            onCheckedChange={setTlsSkipVerify}
          />
          <label
            htmlFor="siem-tls-skip-verify"
            className="inline-flex items-center gap-1 text-sm text-foreground"
          >
            {tlsSkipVerify && (
              <ShieldAlert className="h-3.5 w-3.5 text-status-warning" />
            )}
            Skip TLS verify
          </label>
        </div>
      </div>
    </>
  );
}

function NewForwarderForm() {
  const navigate = useNavigate();
  const create = useCreateSIEMForwarder();

  const [name, setName] = useState("");
  const [transport, setTransport] = useState(TRANSPORTS[0].value);
  const [format, setFormat] = useState(FORMATS[0].value);
  const [endpoint, setEndpoint] = useState("");
  const [auth, setAuth] = useState("");
  const [eventFilters, setEventFilters] = useState("");
  const [batchSize, setBatchSize] = useState(100);
  const [flushIntervalMs, setFlushIntervalMs] = useState(5000);
  const [timeoutSeconds, setTimeoutSeconds] = useState(10);
  const [caCertPem, setCaCertPem] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [tlsSkipVerify, setTlsSkipVerify] = useState(false);

  const handleCreate = async () => {
    const filters = eventFilters
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    const body: SIEMForwarderWriteRequest = {
      name,
      transport,
      endpoint,
      event_filters: filters,
      format,
      tls_skip_verify: tlsSkipVerify,
      batch_size: batchSize,
      flush_interval_ms: flushIntervalMs,
      timeout_seconds: timeoutSeconds,
      enabled,
    };
    if (auth.trim()) body.auth = auth;
    if (caCertPem.trim()) body.ca_cert_pem = caCertPem;

    try {
      await create.mutateAsync(body);
      void navigate({ to: "/dashboard/settings/siem" });
    } catch {
      /* mutation toasts on error */
    }
  };

  return (
    <div className="space-y-6">
      <Card radius="xl" padding="lg" className="space-y-4">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground" htmlFor="siem-name">
            Name
          </label>
          <Input
            id="siem-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="corp-splunk"
            data-initial-focus
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground" htmlFor="siem-transport">
              Transport
            </label>
            <Select
              id="siem-transport"
              value={transport}
              onChange={(e) => setTransport(e.target.value)}
            >
              {TRANSPORTS.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </Select>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground" htmlFor="siem-format">
              Format
            </label>
            <Select
              id="siem-format"
              value={format}
              onChange={(e) => setFormat(e.target.value)}
            >
              {FORMATS.map((t) => (
                <option key={t.value} value={t.value}>
                  {t.label}
                </option>
              ))}
            </Select>
          </div>
        </div>

        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground" htmlFor="siem-endpoint">
            Endpoint
          </label>
          <Input
            id="siem-endpoint"
            value={endpoint}
            onChange={(e) => setEndpoint(e.target.value)}
            placeholder="siem.corp.example.com:6514"
            className="font-mono"
          />
        </div>

        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground" htmlFor="siem-auth">
            Auth
          </label>
          <Input
            id="siem-auth"
            type="password"
            value={auth}
            onChange={(e) => setAuth(e.target.value)}
            placeholder="HEC token / bearer / password"
            className="font-mono"
            autoComplete="new-password"
          />
        </div>

        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground" htmlFor="siem-filters">
            Event filters{" "}
            <span className="text-2xs text-muted-foreground font-normal">
              (comma-separated; blank = all)
            </span>
          </label>
          <Input
            id="siem-filters"
            value={eventFilters}
            onChange={(e) => setEventFilters(e.target.value)}
            placeholder="auth.login.failed, admin.*"
            className="font-mono"
          />
        </div>

        <DeliverySettingsFields
          batchSize={batchSize}
          setBatchSize={setBatchSize}
          flushIntervalMs={flushIntervalMs}
          setFlushIntervalMs={setFlushIntervalMs}
          timeoutSeconds={timeoutSeconds}
          setTimeoutSeconds={setTimeoutSeconds}
          caCertPem={caCertPem}
          setCaCertPem={setCaCertPem}
          enabled={enabled}
          setEnabled={setEnabled}
          tlsSkipVerify={tlsSkipVerify}
          setTlsSkipVerify={setTlsSkipVerify}
        />
      </Card>

      <div className="flex items-center justify-end gap-2">
        <ActionButton
          onClick={() => void navigate({ to: "/dashboard/settings/siem" })}
        >
          Cancel
        </ActionButton>
        <ActionButton
          intent="primary"
          onClick={() => void handleCreate()}
          disabled={create.isPending || !name || !endpoint}
          loading={create.isPending}
        >
          Create Forwarder
        </ActionButton>
      </div>
    </div>
  );
}

function NewForwarderPage() {
  return (
    <SettingsAuthGate>
      <PageShell>
        <RouterLink
          to="/dashboard/settings/siem"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to SIEM forwarders
        </RouterLink>
        <PageHeader
          eyebrow="Settings · SIEM · New"
          title="Add SIEM Forwarder"
        />
        <NewForwarderForm />
      </PageShell>
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute("/dashboard/settings/siem/new/")({
  component: NewForwarderPage,
});
