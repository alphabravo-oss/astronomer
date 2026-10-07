export const TRANSPORTS: { value: string; label: string }[] = [
  { value: "syslog_udp", label: "Syslog (UDP)" },
  { value: "syslog_tcp", label: "Syslog (TCP)" },
  { value: "syslog_tls", label: "Syslog (TLS)" },
  { value: "splunk_hec", label: "Splunk HEC" },
  { value: "ndjson_https", label: "NDJSON over HTTPS" },
];

export const FORMATS: { value: string; label: string }[] = [
  { value: "", label: "Auto (derive from transport)" },
  { value: "rfc5424", label: "Syslog RFC 5424" },
  { value: "rfc3164", label: "Syslog RFC 3164" },
  { value: "cef", label: "CEF" },
  { value: "ndjson", label: "NDJSON" },
];

export function transportLabel(t: string): string {
  return TRANSPORTS.find((x) => x.value === t)?.label ?? t;
}
