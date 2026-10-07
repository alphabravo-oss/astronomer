import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";

import {
  CHIP_COLLAPSE_LIMIT,
  conditionChips,
  podContainerSummary,
  resolveManagedBy,
  resolveOwnerLink,
  resourceAge,
  splitChips,
  type ConditionTone,
} from "@/components/resources/resource-masthead-model";
import type { K8sObject } from "@/components/resources/resource-detail-model";
import { Badge } from "@/components/ui/badge";
import { Tooltip } from "@/components/ui/tooltip";
import type { ResourceDiscoveryView } from "@/lib/api/resources";
import { detailHref } from "@/lib/k8s-paths";
import { toastError, toastSuccess } from "@/lib/toast";
import { cn } from "@/lib/utils";
import { BareButton } from "@/components/form/bare-button";

type MetaItem = { label: string; value: ReactNode };

/** Age with the exact creation timestamp on hover. */
export function ageMetaItem(created?: string): MetaItem | undefined {
  const age = resourceAge(created);
  if (!age) return undefined;
  return {
    label: "Age",
    value: (
      <Tooltip content={age.exact} wrap>
        <time dateTime={created}>{age.relative}</time>
      </Tooltip>
    ),
  };
}

/** Namespace link, age (exact on hover), owner link and managed-by for the masthead meta row. */
export function mastheadMeta(
  obj: K8sObject | undefined,
  clusterId: string,
  namespace: string | undefined,
  discovery?: ResourceDiscoveryView,
): MetaItem[] {
  const items: MetaItem[] = [];
  if (namespace) {
    items.push({
      label: "Namespace",
      value: (
        <Link
          to={detailHref(clusterId, "namespaces", undefined, namespace)}
          className="text-primary hover:underline"
        >
          {namespace}
        </Link>
      ),
    });
  }
  const age = ageMetaItem(obj?.metadata?.creationTimestamp);
  if (age) items.push(age);
  const owner = resolveOwnerLink(obj, clusterId, namespace, discovery);
  if (owner) {
    items.push({
      label: "Owner",
      value: owner.href ? (
        <Link to={owner.href} className="text-primary hover:underline">
          {owner.kind}/{owner.name}
        </Link>
      ) : (
        `${owner.kind}/${owner.name}`
      ),
    });
  }
  const managed = resolveManagedBy(obj);
  if (managed) {
    items.push({
      label: "Managed by",
      value: managed.detail
        ? `${managed.label} ${managed.detail}`
        : managed.label,
    });
  }
  return items;
}

async function copyPair(pair: string) {
  try {
    await navigator.clipboard.writeText(pair);
    toastSuccess(`Copied ${pair}`);
  } catch {
    toastError("Could not copy to the clipboard");
  }
}

function ChipGroup({
  title,
  entries,
}: {
  title: string;
  entries: Array<[string, string]>;
}) {
  const [expanded, setExpanded] = useState(false);
  if (entries.length === 0) return null;
  const { visible, hidden } = splitChips(entries, expanded);
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="text-xs font-medium text-muted-foreground">{title}</span>
      {visible.map(([key, value]) => (
        <Tooltip key={key} content="Click to copy">
          <BareButton
            type="button"
            onClick={() => void copyPair(`${key}=${value}`)}
            className="max-w-xs truncate rounded-md border border-border bg-muted/40 px-2 py-0.5 font-mono text-2xs text-foreground transition-colors hover:bg-accent"
            aria-label={`Copy ${key}=${value}`}
          >
            {key}={value}
          </BareButton>
        </Tooltip>
      ))}
      {hidden > 0 && (
        <BareButton
          type="button"
          onClick={() => setExpanded(true)}
          className="rounded-md px-2 py-0.5 text-2xs font-medium text-primary hover:underline"
        >
          +{hidden} more
        </BareButton>
      )}
      {expanded && entries.length > CHIP_COLLAPSE_LIMIT && (
        <BareButton
          type="button"
          onClick={() => setExpanded(false)}
          className="rounded-md px-2 py-0.5 text-2xs font-medium text-muted-foreground hover:underline"
        >
          Show less
        </BareButton>
      )}
    </div>
  );
}

const TONE_CLASS: Record<ConditionTone, string> = {
  ok: "border-status-success/30 bg-status-success/10 text-status-success",
  bad: "border-status-error/30 bg-status-error/10 text-status-error",
  unknown: "border-border bg-muted text-muted-foreground",
};

export function ConditionsStrip({
  conditions,
}: {
  conditions: Array<Record<string, unknown>> | undefined;
}) {
  const chips = conditionChips(conditions);
  if (chips.length === 0) return null;
  return (
    <ul className="flex flex-wrap items-center gap-1.5" aria-label="Conditions">
      {chips.map((chip) => (
        <li key={chip.key}>
          <Tooltip content={chip.detail || `${chip.type}: ${chip.status}`} wrap>
            <span
              data-tone={chip.tone}
              className={cn(
                "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-2xs font-medium",
                TONE_CLASS[chip.tone],
              )}
            >
              {chip.type}
              <span className="opacity-70">{chip.status}</span>
            </span>
          </Tooltip>
        </li>
      ))}
    </ul>
  );
}

export function PodContainerSummary({ obj }: { obj?: K8sObject }) {
  const summary = podContainerSummary(obj);
  if (!summary) return null;
  return (
    <dl className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted-foreground">
      <div>
        <dt className="inline">Ready: </dt>
        <dd className="inline font-mono tabular-nums text-foreground">
          {summary.ready}/{summary.total}
        </dd>
      </div>
      <div>
        <dt className="inline">Restarts: </dt>
        <dd
          className={cn(
            "inline tabular-nums",
            summary.restarts > 0 ? "text-status-warning" : "text-foreground",
          )}
        >
          {summary.restarts}
        </dd>
      </div>
      {summary.lastTermination && (
        <div>
          <dt className="inline">Last termination: </dt>
          <dd className="inline text-foreground">{summary.lastTermination}</dd>
        </div>
      )}
    </dl>
  );
}

/** Chips, conditions strip and (for pods) the container summary below the meta line. */
export function MastheadDetails({
  obj,
  isPod,
}: {
  obj?: K8sObject;
  isPod: boolean;
}) {
  return (
    <>
      {isPod && <PodContainerSummary obj={obj} />}
      <ConditionsStrip conditions={obj?.status?.conditions} />
      <ChipGroup
        title="Labels"
        entries={Object.entries(obj?.metadata?.labels ?? {})}
      />
      <ChipGroup
        title="Annotations"
        entries={Object.entries(obj?.metadata?.annotations ?? {})}
      />
    </>
  );
}

export function KindBadge({ kind }: { kind: string }) {
  return <Badge variant="secondary">{kind}</Badge>;
}
