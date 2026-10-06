import { format, parseISO } from "date-fns";

import type { K8sObject } from "@/components/resources/resource-detail-model";
import { podSpecPath } from "@/components/resources/guided-resource-model";
import { ownerHref } from "@/components/resources/related-resource-model";
import type { ResourceDiscoveryView } from "@/lib/api/resources";
import { formatRelativeTime } from "@/lib/utils";

/**
 * Pure derivations behind the resource detail masthead. Everything here reads
 * only what the live object's metadata states; nothing is inferred.
 */

/** Effective pod status: first waiting/failed-termination reason, else status.reason, else phase. */
export function effectivePodStatus(obj?: K8sObject): string | undefined {
  for (const status of [
    ...(obj?.status?.initContainerStatuses ?? []),
    ...(obj?.status?.containerStatuses ?? []),
  ]) {
    const waiting = status.state?.waiting;
    if (waiting?.reason) return waiting.reason;
    const terminated = status.state?.terminated;
    if (terminated?.reason && (terminated.exitCode ?? 0) !== 0) {
      return terminated.reason;
    }
  }
  return obj?.status?.reason ?? obj?.status?.phase;
}

export interface ResourceAge {
  relative: string;
  /** Exact local timestamp for the tooltip. */
  exact: string;
}

export function resourceAge(created?: string): ResourceAge | undefined {
  if (!created) return undefined;
  try {
    return {
      relative: formatRelativeTime(created),
      exact: format(parseISO(created), "PPpp"),
    };
  } catch {
    return { relative: formatRelativeTime(created), exact: created };
  }
}

export interface OwnerLink {
  kind: string;
  name: string;
  /** Present only when the owner kind has a detail route in the console. */
  href?: string;
}

/**
 * The controlling owner (or first owner) reference. The link comes from live
 * discovery (same resolver as the Related tab); without discovery the owner is
 * shown as text rather than guessing a route.
 */
export function resolveOwnerLink(
  obj: K8sObject | undefined,
  clusterId: string,
  namespace?: string,
  discovery?: ResourceDiscoveryView,
): OwnerLink | undefined {
  const refs = obj?.metadata?.ownerReferences ?? [];
  const ref = refs.find((item) => item.controller) ?? refs[0];
  if (!ref?.kind || !ref.name) return undefined;
  return {
    kind: ref.kind,
    name: ref.name,
    href: discovery
      ? ownerHref(clusterId, namespace, ref, discovery)
      : undefined,
  };
}

export type ManagedBySource =
  "helm" | "flux-kustomization" | "flux-helmrelease" | "astronomer";

export interface ManagedBy {
  source: ManagedBySource;
  label: string;
  detail: string;
}

/** Ownership as stated by well-known labels/annotations; undefined when not knowable. */
export function resolveManagedBy(obj?: K8sObject): ManagedBy | undefined {
  const labels = obj?.metadata?.labels ?? {};
  const annotations = obj?.metadata?.annotations ?? {};
  const qualified = (ns: string | undefined, name: string) =>
    ns ? `${ns}/${name}` : name;

  const ksName = labels["kustomize.toolkit.fluxcd.io/name"];
  if (ksName) {
    return {
      source: "flux-kustomization",
      label: "Flux Kustomization",
      detail: qualified(
        labels["kustomize.toolkit.fluxcd.io/namespace"],
        ksName,
      ),
    };
  }
  const hrName = labels["helm.toolkit.fluxcd.io/name"];
  if (hrName) {
    return {
      source: "flux-helmrelease",
      label: "Flux HelmRelease",
      detail: qualified(labels["helm.toolkit.fluxcd.io/namespace"], hrName),
    };
  }
  const release = annotations["meta.helm.sh/release-name"];
  if (release || labels["app.kubernetes.io/managed-by"] === "Helm") {
    return {
      source: "helm",
      label: "Helm release",
      detail: release
        ? qualified(annotations["meta.helm.sh/release-namespace"], release)
        : "",
    };
  }
  if (
    labels["app.kubernetes.io/managed-by"] === "astronomer" ||
    labels["astronomer.io/managed-by"] === "astronomer"
  ) {
    return { source: "astronomer", label: "Astronomer", detail: "platform" };
  }
  return undefined;
}

export type ConditionTone = "ok" | "bad" | "unknown";

export interface ConditionChip {
  key: string;
  type: string;
  status: string;
  tone: ConditionTone;
  /** Tooltip body: reason and message when present. */
  detail: string;
}

// Conditions whose healthy value is "False" (pressure/failure style).
const NEGATIVE_POLARITY =
  /(pressure|unavailable|failure|failed|stalled|reconciling)$/i;

export function conditionChips(
  conditions: Array<Record<string, unknown>> | undefined,
): ConditionChip[] {
  return (conditions ?? []).flatMap((condition, index) => {
    const type = typeof condition.type === "string" ? condition.type : "";
    if (!type) return [];
    const status = typeof condition.status === "string" ? condition.status : "";
    const tone: ConditionTone =
      status === "True" || status === "False"
        ? (status === "True") === !NEGATIVE_POLARITY.test(type)
          ? "ok"
          : "bad"
        : "unknown";
    const reason = typeof condition.reason === "string" ? condition.reason : "";
    const message =
      typeof condition.message === "string" ? condition.message : "";
    return [
      {
        key: `${type}-${index}`,
        type,
        status: status || "Unknown",
        tone,
        detail: [reason, message].filter(Boolean).join(": "),
      },
    ];
  });
}

export interface ContainerSummary {
  ready: number;
  total: number;
  restarts: number;
  /** "<reason> (exit N)" of the most recent termination across containers. */
  lastTermination?: string;
}

export function podContainerSummary(
  obj?: K8sObject,
): ContainerSummary | undefined {
  const statuses = obj?.status?.containerStatuses;
  if (!statuses?.length) return undefined;
  let latest: { at: number; text: string } | undefined;
  for (const status of statuses) {
    const term = status.lastState?.terminated;
    if (!term?.reason) continue;
    const at = term.finishedAt ? Date.parse(term.finishedAt) : 0;
    const text =
      term.exitCode == null
        ? term.reason
        : `${term.reason} (exit ${term.exitCode})`;
    if (!latest || at >= latest.at) latest = { at, text };
  }
  return {
    ready: statuses.filter((s) => s.ready).length,
    total: statuses.length,
    restarts: statuses.reduce((sum, s) => sum + (s.restartCount ?? 0), 0),
    lastTermination: latest?.text,
  };
}

/** Kinds whose edit surface offers a guided form (mirrors the guided form sections). */
const GUIDED_KINDS = new Set([
  "Service",
  "Ingress",
  "NetworkPolicy",
  "Secret",
  "PersistentVolumeClaim",
  "HorizontalPodAutoscaler",
  "PodDisruptionBudget",
  "Role",
  "ClusterRole",
  "RoleBinding",
  "ClusterRoleBinding",
  "Gateway",
]);

export function hasGuidedEditForm(kind?: string): boolean {
  return !!kind && (podSpecPath(kind) !== null || GUIDED_KINDS.has(kind));
}

export const CHIP_COLLAPSE_LIMIT = 6;

export function splitChips<T>(
  items: T[],
  expanded: boolean,
  limit = CHIP_COLLAPSE_LIMIT,
): { visible: T[]; hidden: number } {
  if (expanded || items.length <= limit) return { visible: items, hidden: 0 };
  return { visible: items.slice(0, limit), hidden: items.length - limit };
}
