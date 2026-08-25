import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  asRecord,
  displayNumber,
  type K8sObject,
} from "@/components/resources/resource-detail-model";
import {
  KeyValueTable,
  Section,
} from "@/components/resources/resource-overview-primitives";

export function SecretOverview({ obj }: { obj: K8sObject }) {
  const type = asRecord(obj).type;
  const keys = Object.keys(obj.data ?? {});
  const summary: Array<[string, string]> = [];
  if (type) summary.push(["type", String(type)]);
  summary.push(["keys", String(keys.length)]);
  return (
    <>
      <Section title="Secret">
        <KeyValueTable entries={summary} />
      </Section>
      {keys.length > 0 && (
        <Section title="Keys">
          <ul className="space-y-1">
            {keys.map((k) => (
              <li key={k} className="font-mono text-xs text-foreground">
                {k}
              </li>
            ))}
          </ul>
        </Section>
      )}
    </>
  );
}
export function PVOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const claim = asRecord(spec.claimRef);
  const summary: Array<[string, string]> = [];
  if (status.phase) summary.push(["status", String(status.phase)]);
  const cap = asRecord(spec.capacity).storage;
  if (cap) summary.push(["capacity", String(cap)]);
  if (Array.isArray(spec.accessModes))
    summary.push(["accessModes", (spec.accessModes as string[]).join(", ")]);
  if (spec.persistentVolumeReclaimPolicy)
    summary.push(["reclaimPolicy", String(spec.persistentVolumeReclaimPolicy)]);
  if (spec.storageClassName)
    summary.push(["storageClass", String(spec.storageClassName)]);
  if (claim.name)
    summary.push([
      "claim",
      `${claim.namespace ? `${claim.namespace}/` : ""}${claim.name}`,
    ]);
  return (
    <Section title="PersistentVolume">
      <KeyValueTable entries={summary} />
    </Section>
  );
}

export function NetworkPolicyOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const summary: Array<[string, string]> = [];
  if (Array.isArray(spec.policyTypes))
    summary.push(["policyTypes", (spec.policyTypes as string[]).join(", ")]);
  summary.push([
    "ingress rules",
    String(Array.isArray(spec.ingress) ? spec.ingress.length : 0),
  ]);
  summary.push([
    "egress rules",
    String(Array.isArray(spec.egress) ? spec.egress.length : 0),
  ]);
  const podSelector = Object.entries(
    asRecord(asRecord(spec.podSelector).matchLabels),
  ) as Array<[string, string]>;
  return (
    <>
      <Section title="NetworkPolicy">
        <KeyValueTable entries={summary} />
      </Section>
      <Section title="Pod Selector">
        <KeyValueTable entries={podSelector} />
      </Section>
    </>
  );
}

export function StorageClassOverview({ obj }: { obj: K8sObject }) {
  const o = asRecord(obj); // StorageClass fields are top-level, not under spec.
  const summary: Array<[string, string]> = [];
  if (o.provisioner) summary.push(["provisioner", String(o.provisioner)]);
  if (o.reclaimPolicy) summary.push(["reclaimPolicy", String(o.reclaimPolicy)]);
  if (o.volumeBindingMode)
    summary.push(["volumeBindingMode", String(o.volumeBindingMode)]);
  if (o.allowVolumeExpansion != null)
    summary.push([
      "allowVolumeExpansion",
      o.allowVolumeExpansion ? "Yes" : "No",
    ]);
  const params = Object.entries(asRecord(o.parameters)) as Array<
    [string, string]
  >;
  return (
    <>
      <Section title="StorageClass">
        <KeyValueTable entries={summary} />
      </Section>
      {params.length > 0 && (
        <Section title="Parameters">
          <KeyValueTable entries={params} />
        </Section>
      )}
    </>
  );
}

export function RoleOverview({ obj }: { obj: K8sObject }) {
  const rules = Array.isArray(asRecord(obj).rules)
    ? (asRecord(obj).rules as Array<Record<string, unknown>>)
    : [];
  const join = (v: unknown, blankAs?: string) =>
    (Array.isArray(v) ? v : [])
      .map((x) => (x === "" && blankAs ? blankAs : String(x)))
      .join(", ") || "-";
  return (
    <Section title="Rules">
      {rules.length === 0 ? (
        <p className="text-xs text-muted-foreground">No rules.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>API Groups</TableHead>
              <TableHead>Resources</TableHead>
              <TableHead>Verbs</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rules.map((r, i) => (
              <TableRow key={i}>
                <TableCell className="font-mono text-xs">
                  {join(r.apiGroups, "core")}
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {join(r.resources)}
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">
                  {join(r.verbs)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Section>
  );
}

export function RoleBindingOverview({ obj }: { obj: K8sObject }) {
  const o = asRecord(obj);
  const roleRef = asRecord(o.roleRef);
  const subjects = Array.isArray(o.subjects)
    ? (o.subjects as Array<Record<string, unknown>>)
    : [];
  return (
    <>
      <Section title="Role Reference">
        <KeyValueTable
          entries={[
            ["kind", String(roleRef.kind ?? "-")],
            ["name", String(roleRef.name ?? "-")],
          ]}
        />
      </Section>
      <Section title="Subjects">
        {subjects.length === 0 ? (
          <p className="text-xs text-muted-foreground">None</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Kind</TableHead>
                <TableHead>Name</TableHead>
                <TableHead>Namespace</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {subjects.map((s, i) => (
                <TableRow key={`${String(s.name ?? "")}-${i}`}>
                  <TableCell className="text-xs">
                    {String(s.kind ?? "-")}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {String(s.name ?? "-")}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {String(s.namespace ?? "-")}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Section>
    </>
  );
}

export function ServiceAccountOverview({ obj }: { obj: K8sObject }) {
  const o = asRecord(obj);
  const secrets = Array.isArray(o.secrets)
    ? (o.secrets as Array<Record<string, unknown>>)
    : [];
  const summary: Array<[string, string]> = [
    ["automountToken", o.automountServiceAccountToken === false ? "No" : "Yes"],
    ["secrets", String(secrets.length)],
  ];
  return (
    <>
      <Section title="ServiceAccount">
        <KeyValueTable entries={summary} />
      </Section>
      {secrets.length > 0 && (
        <Section title="Secrets">
          <ul className="space-y-1">
            {secrets.map((s, i) => (
              <li
                key={`${String(s.name ?? "")}-${i}`}
                className="font-mono text-xs text-foreground"
              >
                {String(s.name ?? "-")}
              </li>
            ))}
          </ul>
        </Section>
      )}
    </>
  );
}

export function PDBOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const summary: Array<[string, string]> = [];
  if (spec.minAvailable != null)
    summary.push(["minAvailable", String(spec.minAvailable)]);
  if (spec.maxUnavailable != null)
    summary.push(["maxUnavailable", String(spec.maxUnavailable)]);
  summary.push([
    "healthy",
    `${displayNumber(status.currentHealthy, "0")}/${displayNumber(status.desiredHealthy, "0")}`,
  ]);
  if (status.disruptionsAllowed != null)
    summary.push(["disruptionsAllowed", String(status.disruptionsAllowed)]);
  return (
    <Section title="PodDisruptionBudget">
      <KeyValueTable entries={summary} />
    </Section>
  );
}

export function ResourceQuotaOverview({ obj }: { obj: K8sObject }) {
  const status = asRecord(obj.status);
  const hard = asRecord(
    Object.keys(asRecord(status.hard)).length
      ? status.hard
      : asRecord(obj.spec).hard,
  );
  const used = asRecord(status.used);
  const rows = Object.keys(hard).map(
    (k) => [
      k,
      `${displayNumber(used[k], "0")} / ${displayNumber(hard[k])}`,
    ] as [string, string],
  );
  return (
    <Section title="Quota (used / hard)">
      <KeyValueTable entries={rows} />
    </Section>
  );
}

export function CRDOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const names = asRecord(spec.names);
  const versions = Array.isArray(spec.versions)
    ? (spec.versions as Array<Record<string, unknown>>)
    : [];
  const summary: Array<[string, string]> = [];
  if (spec.group) summary.push(["group", String(spec.group)]);
  if (spec.scope) summary.push(["scope", String(spec.scope)]);
  if (names.kind) summary.push(["kind", String(names.kind)]);
  if (names.plural) summary.push(["plural", String(names.plural)]);
  const vers = versions
    .map((v) => String(v.name ?? ""))
    .filter(Boolean)
    .join(", ");
  if (vers) summary.push(["versions", vers]);
  return (
    <Section title="CustomResourceDefinition">
      <KeyValueTable entries={summary} />
    </Section>
  );
}

export function GatewayOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const status = asRecord(obj.status);
  const summary: Array<[string, string]> = [];
  if (spec.gatewayClassName)
    summary.push(["gatewayClass", String(spec.gatewayClassName)]);
  const addrs = (Array.isArray(status.addresses) ? status.addresses : [])
    .map((a) => String(asRecord(a).value ?? ""))
    .filter(Boolean);
  if (addrs.length) summary.push(["addresses", addrs.join(", ")]);
  const listeners = Array.isArray(spec.listeners)
    ? (spec.listeners as Array<Record<string, unknown>>)
    : [];
  return (
    <>
      <Section title="Gateway">
        <KeyValueTable entries={summary} />
      </Section>
      {listeners.length > 0 && (
        <Section title="Listeners">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Port</TableHead>
                <TableHead>Protocol</TableHead>
                <TableHead>Hostname</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {listeners.map((l, i) => (
                <TableRow key={String(l.name ?? i)}>
                  <TableCell className="text-xs font-medium">
                    {String(l.name ?? "-")}
                  </TableCell>
                  <TableCell className="text-xs tabular-nums">
                    {String(l.port ?? "-")}
                  </TableCell>
                  <TableCell className="text-xs">
                    {String(l.protocol ?? "-")}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {String(l.hostname ?? "*")}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Section>
      )}
    </>
  );
}

export function RouteOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const parents = (
    Array.isArray(spec.parentRefs)
      ? (spec.parentRefs as Array<Record<string, unknown>>)
      : []
  )
    .map((p) => {
      const r = asRecord(p);
      return `${r.namespace ? `${r.namespace}/` : ""}${String(r.name ?? "")}`.trim();
    })
    .filter(Boolean);
  const hostnames = Array.isArray(spec.hostnames)
    ? (spec.hostnames as string[])
    : [];
  const summary: Array<[string, string]> = [];
  if (parents.length) summary.push(["parents", parents.join(", ")]);
  summary.push([
    "rules",
    String(Array.isArray(spec.rules) ? spec.rules.length : 0),
  ]);
  return (
    <>
      <Section title="Route">
        <KeyValueTable entries={summary} />
      </Section>
      {hostnames.length > 0 && (
        <Section title="Hostnames">
          <ul className="space-y-1">
            {hostnames.map((h) => (
              <li key={h} className="font-mono text-xs text-foreground">
                {h}
              </li>
            ))}
          </ul>
        </Section>
      )}
    </>
  );
}

export function GatewayClassOverview({ obj }: { obj: K8sObject }) {
  const spec = asRecord(obj.spec);
  const summary: Array<[string, string]> = [];
  if (spec.controllerName)
    summary.push(["controller", String(spec.controllerName)]);
  if (spec.description) summary.push(["description", String(spec.description)]);
  return (
    <Section title="GatewayClass">
      <KeyValueTable entries={summary} />
    </Section>
  );
}
