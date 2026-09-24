import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import {
  useK8sResource,
  useResourceDiscovery,
} from "@/lib/hooks/kubernetes-proxy";
import { useClusterResourcePermission } from "@/lib/permission-hooks";
import { detailHref } from "@/lib/k8s-paths";
import { PermissionState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { ActionButton } from "@/components/ui/action-button";
import type { K8sObject } from "./resource-detail-model";
import {
  CHILD_RESOURCE_TYPES,
  ownerHref,
  ownedBy,
  relatedPagePath,
  serviceSelectsPod,
} from "./related-resource-model";

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-3 rounded-lg border border-border bg-card p-4">
      <h2 className="text-sm font-semibold">{title}</h2>
      {children}
    </section>
  );
}

function RelatedPage({
  clusterId,
  namespace,
  resourceType,
  matches,
}: {
  clusterId: string;
  namespace: string;
  resourceType: string;
  matches: (item: K8sObject) => boolean;
}) {
  const [tokens, setTokens] = useState([""]);
  const permission = useClusterResourcePermission(
    clusterId,
    resourceType,
    "read",
  );
  const query = useK8sResource(
    clusterId,
    relatedPagePath(resourceType, namespace, tokens.at(-1) ?? ""),
    permission.allowed,
  );
  const continuation = query.isError
    ? undefined
    : query.data?.metadata?.continue;
  if (!permission.allowed)
    return <PermissionState permission={`${resourceType}:read`} />;
  return (
    <>
      <QueryStates
        query={query}
        permission={`${resourceType}:read`}
        errorTitle="Relationships unavailable"
      >
        {(data: { items?: K8sObject[]; metadata?: { continue?: string } }) => {
          const rows = (data.items ?? []).filter(matches);
          return (
            <>
              <p className="text-xs text-muted-foreground">
                Page {tokens.length} of namespace {namespace}; checking at most
                50 {resourceType} per request. More relationships may exist on
                later pages.
              </p>
              {rows.length ? (
                <ul className="space-y-2">
                  {rows.map((row) => (
                    <li key={row.metadata?.uid ?? row.metadata?.name}>
                      <Link
                        className="font-mono text-xs underline"
                        to={detailHref(
                          clusterId,
                          resourceType,
                          namespace,
                          row.metadata?.name ?? "",
                        )}
                      >
                        {row.metadata?.name}
                      </Link>
                    </li>
                  ))}
                </ul>
              ) : (
                <p className="text-sm text-muted-foreground">
                  No matching relationships on this page.
                </p>
              )}
            </>
          );
        }}
      </QueryStates>
      <div className="flex gap-2">
        <ActionButton
          disabled={tokens.length === 1 || query.isFetching}
          onClick={() => setTokens((current) => current.slice(0, -1))}
        >
          Previous
        </ActionButton>
        <ActionButton
          disabled={!continuation || query.isFetching}
          onClick={() => {
            const token = continuation;
            if (token) setTokens((current) => [...current, token]);
          }}
        >
          Next page
        </ActionButton>
      </div>
    </>
  );
}

export function RelatedResources({
  clusterId,
  namespace,
  name,
  kind,
  obj,
}: {
  clusterId: string;
  namespace?: string;
  name: string;
  kind: string;
  obj?: K8sObject;
}) {
  const discovery = useResourceDiscovery(clusterId);
  const owners = obj?.metadata?.ownerReferences ?? [];
  const children = CHILD_RESOURCE_TYPES[kind];
  return (
    <div className="space-y-6">
      <Section title="Owned By">
        {!owners.length ? (
          <p className="text-xs text-muted-foreground">No owner references.</p>
        ) : (
          <QueryStates
            query={discovery}
            errorTitle="Owner discovery unavailable"
          >
            {(data) => (
              <ul className="space-y-2">
                {owners.map((owner) => {
                  const href = ownerHref(clusterId, namespace, owner, data);
                  return (
                    <li
                      key={
                        owner.uid ||
                        `${owner.apiVersion}/${owner.kind}/${owner.name}`
                      }
                      className="text-sm"
                    >
                      {owner.kind}{" "}
                      {href ? (
                        <Link className="font-mono underline" to={href}>
                          {owner.name}
                        </Link>
                      ) : (
                        <span>
                          {owner.name} — resource version or scope not available
                          in discovery
                        </span>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </QueryStates>
        )}
      </Section>
      {children && namespace && (
        <Section title={`Owned ${children}`}>
          <RelatedPage
            key={`${clusterId}/${namespace}/${name}/${obj?.metadata?.uid}/${children}`}
            clusterId={clusterId}
            namespace={namespace}
            resourceType={children}
            matches={(child) =>
              ownedBy(child, {
                name,
                kind,
                uid: obj?.metadata?.uid,
                apiVersion: obj?.apiVersion,
              })
            }
          />
        </Section>
      )}
      {kind === "Pod" && namespace && (
        <Section title="Selected by Services">
          <RelatedPage
            key={`${clusterId}/${namespace}/${name}/services`}
            clusterId={clusterId}
            namespace={namespace}
            resourceType="services"
            matches={(service) => serviceSelectsPod(service, obj)}
          />
        </Section>
      )}
      {kind === "Service" && namespace && (
        <Section title="Selected Pods">
          {Object.keys(obj?.spec?.selector ?? {}).length ? (
            <RelatedPage
              key={`${clusterId}/${namespace}/${name}/pods`}
              clusterId={clusterId}
              namespace={namespace}
              resourceType="pods"
              matches={(pod) => serviceSelectsPod(obj ?? {}, pod)}
            />
          ) : (
            <p className="text-sm text-muted-foreground">
              This Service has no Pod selector. Its endpoints may be managed
              separately.
            </p>
          )}
        </Section>
      )}
    </div>
  );
}
