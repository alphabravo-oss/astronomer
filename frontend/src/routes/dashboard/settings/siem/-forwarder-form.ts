import type { SIEMForwarder } from "@/types";
import {
  SIEM_AUTH_SENTINEL,
  type SIEMForwarderWriteRequest,
} from "@/lib/api/siem-forwarders";
export function forwarderDefaults(forwarder: SIEMForwarder | null) {
  return {
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
  };
}
export function forwarderRequest(
  value: ReturnType<typeof forwarderDefaults>,
  forwarder: SIEMForwarder | null,
): SIEMForwarderWriteRequest {
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
  } else if (forwarder?.authConfigured) {
    body.auth = SIEM_AUTH_SENTINEL;
  }
  if (value.caCertPem.trim()) {
    body.ca_cert_pem = value.caCertPem;
  }

  return body;
}
