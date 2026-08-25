import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

// --- Alerting Types ---

export type AlertSeverity =
  OpenAPIComponents["schemas"]["AlertRule"]["severity"];

export type AlertRuleType = OpenAPIComponents["schemas"]["AlertRule"]["type"];

// Sprint 072 — rule_kind switches the evaluator path.
// "threshold" is the existing static-threshold logic; "anomaly"
// uses the rolling-baseline + stddev check from anomaly_baselines.
export type AlertRuleKind =
  OpenAPIComponents["schemas"]["AlertRule"]["ruleKind"];

// Direction the anomaly rule fires on:
//  - above:  current > mean + N*stddev
//  - below:  current < mean - N*stddev
//  - either: |current - mean| > N*stddev
export type AnomalyDirection = Exclude<
  OpenAPIComponents["schemas"]["AlertRule"]["anomalyDirection"],
  ""
>;

/** Exact camelCase response DTO emitted by alertRuleResponseFields. */
export type AlertRule = OpenAPIComponents["schemas"]["AlertRule"];

/** Exact read-only response DTO emitted by anomalyBaselineResponse. */
export type AnomalyBaseline = OpenAPIComponents["schemas"]["AnomalyBaseline"];

export type AlertEventStatus =
  OpenAPIComponents["schemas"]["AlertEvent"]["status"];

/** Exact camelCase response DTO emitted by alertEventResponseFields. */
export type AlertEvent = OpenAPIComponents["schemas"]["AlertEvent"];

export type NotificationChannel =
  OpenAPIComponents["schemas"]["NotificationChannel"];
export type NotificationChannelType = NotificationChannel["type"];

/** Exact camelCase response DTO emitted by alertSilenceResponse. */
export type AlertSilence = OpenAPIComponents["schemas"]["AlertSilence"];

// --- Alertmanager-style inhibition rules (P-03) ---
//
// Mirrors the control_plane_silences model. A firing SOURCE alert (matching
// source_matchers) suppresses dispatch of any TARGET alert (matching
// target_matchers) that shares an equal value on every label in equal_labels.
// Generated wire shapes remain snake_case; the API adapter maps them.
export type InhibitionMatcher = CamelizeKeys<
  OpenAPIComponents["schemas"]["InhibitionMatcherResponse"]
>;

export type AlertInhibition = CamelizeKeys<
  OpenAPIComponents["schemas"]["InhibitionResponse"]
>;

// --- SIEM forwarders (F-05) ---
//
// External SIEM destinations (syslog / Splunk HEC / NDJSON-HTTPS). The auth
// blob is write-only; reads return the `<encrypted>` sentinel and
// `authConfigured` reflects whether one is stored.
export type SIEMTransport =
  "syslog_udp" | "syslog_tcp" | "syslog_tls" | "splunk_hec" | "ndjson_https";

export interface SIEMForwarder {
  id: string;
  name: string;
  transport: SIEMTransport | string;
  endpoint: string;
  auth: string;
  authConfigured: boolean;
  eventFilters: string[];
  format: string;
  tlsSkipVerify: boolean;
  caCertConfigured: boolean;
  batchSize: number;
  flushIntervalMs: number;
  timeoutSeconds: number;
  enabled: boolean;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
}

export interface SIEMForwarderStatus {
  forwarderId: string;
  lastSentAt: string | null;
  lastError: string;
  queueDepth: number;
  droppedTotal: number;
  dispatchedTotal: number;
  updatedAt: string;
}

// --- SCIM provisioning tokens (F-05) ---
//
// The plaintext token is returned exactly once at creation time; list rows
// only ever carry metadata.
export type SCIMToken = CamelizeKeys<OpenAPIComponents["schemas"]["SCIMToken"]>;

export type SCIMTokenCreated = CamelizeKeys<
  OpenAPIComponents["schemas"]["SCIMTokenCreated"]
>;

// --- Gatekeeper / OPA constraint authoring (P-04) ---
export type GatekeeperConstraint = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["GatekeeperConstraintSummary"]>,
  "enforcementAction" | "violationCount"
> & {
  source: "bundle" | "custom";
  enforcementAction: string;
  violationCount: number;
};

export type ConstraintValidateResult = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ConstraintValidationResponse"]>,
  "name" | "kind"
> & {
  name: string;
  kind: string;
};
