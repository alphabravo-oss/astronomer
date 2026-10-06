import { expect, test, type Page } from "@playwright/test";

import { seedAuth } from "./helpers/auth";
import { installStubs } from "../e2e-smoke/stubs";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
import type { StubOverride } from "../e2e-smoke/stub-overrides";

// Plan 031 P6b section 3: networking / storage / policy / RBAC tables render
// worst-case values at 1280px with no silently clipped cell, no clipped header
// and no column wider than 3x its content (the single `grow` column is exempt).

const C = SMOKE_CLUSTER_ID;
const DAY = 86_400_000;
const ago = (days: number) => new Date(Date.now() - days * DAY).toISOString();
const AGES = [0.01, 3, 40, 330, 900];
const WIRE_NOW = ago(2);

const LONG_NS = "cert-manager-webhook-system";
const LONG_NAME = "payments-gateway-internal-grpc-headless-service";

function rows<T>(count: number, make: (i: number) => T): T[] {
  return Array.from({ length: count }, (_, i) => make(i));
}

const pick = <T>(values: T[], i: number) => values[i % values.length];

function envelope(data: unknown[]) {
  return {
    data,
    pagination: {
      total: data.length,
      limit: 100,
      offset: 0,
      has_more: false,
      next_offset: null,
      next_cursor: null,
    },
  };
}

const NAMES = ["web", "kube-dns", LONG_NAME, "otel-collector-agent", "db-0"];
const NAMESPACES = [
  "default",
  "kube-system",
  LONG_NS,
  "payments",
  "monitoring",
];

const named: Record<string, (i: number) => Record<string, unknown>> = {
  services: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    type: pick(
      ["ClusterIP", "NodePort", "LoadBalancer", "ClusterIP", "ExternalName"],
      i,
    ),
    clusterIP: i === 4 ? "None" : `10.100.200.${250 - i}`,
    ports: rows(i === 2 ? 6 : 2, (p) => ({
      port: 8000 + p * 443,
      protocol: "TCP",
      targetPort: 8080,
    })),
    createdAt: ago(AGES[i]),
  }),
  ingresses: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    ingressClass: pick(
      ["nginx", "traefik", "alb", "internal-nginx-controller", ""],
      i,
    ),
    hosts:
      i === 2
        ? [
            "payments-api.eu-central-1.internal.example-corp.com",
            "payments-admin.eu-central-1.internal.example-corp.com",
            "payments-legacy.example-corp.com",
          ]
        : i === 4
          ? []
          : [`app${i}.example.com`],
    paths: [],
    tls: i % 2 === 0,
    createdAt: ago(AGES[i]),
  }),
  networkpolicies: (i) => ({
    name:
      i === 2 ? "allow-ingress-from-payments-gateway-and-monitoring" : NAMES[i],
    namespace: NAMESPACES[i],
    podSelector: {},
    policyTypes: i % 2 === 0 ? ["Ingress", "Egress"] : ["Ingress"],
    ingressRules: i * 7,
    egressRules: i * 3,
    createdAt: ago(AGES[i]),
  }),
  persistentvolumes: (i) => ({
    name: `pvc-3f9a2c1e-7b4d-4c1a-9e55-0d8f6a12b7d${i}`,
    status: pick(["Bound", "Available", "Released", "Bound", "Failed"], i),
    capacity: pick(["10Gi", "100Gi", "1Ti", "500Mi", "20Gi"], i),
    accessModes: pick(
      [
        ["ReadWriteOnce"],
        ["ReadWriteMany"],
        ["ReadWriteOnce", "ReadOnlyMany"],
        ["ReadWriteOncePod"],
        ["ReadOnlyMany"],
      ],
      i,
    ),
    storageClass: pick(
      ["gp3", "rancher.io-local-path", "premium-ssd-encrypted", "standard", ""],
      i,
    ),
    claimRef: `${NAMESPACES[i]}/data-${NAMES[i]}-0`,
    createdAt: ago(AGES[i]),
  }),
  persistentvolumeclaims: (i) => ({
    name: `data-${NAMES[i]}-0`,
    namespace: NAMESPACES[i],
    status: pick(["Bound", "Pending", "Bound", "Lost", "Bound"], i),
    capacity: pick(["10Gi", "100Gi", "1Ti", "500Mi", "20Gi"], i),
    storageClass: pick(
      ["gp3", "rancher.io-local-path", "premium-ssd-encrypted", "standard", ""],
      i,
    ),
    volumeName: i === 1 ? "" : `pvc-3f9a2c1e-7b4d-4c1a-9e55-0d8f6a12b7d${i}`,
    createdAt: ago(AGES[i]),
  }),
  storageclasses: (i) => ({
    name: pick(
      [
        "gp3",
        "local-path",
        "premium-ssd-encrypted-retain",
        "standard",
        "longhorn",
      ],
      i,
    ),
    provisioner: pick(
      [
        "ebs.csi.aws.com",
        "rancher.io/local-path",
        "disk.csi.azure.com",
        "kubernetes.io/no-provisioner",
        "driver.longhorn.io",
      ],
      i,
    ),
    reclaimPolicy: pick(["Delete", "Retain", "Delete", "Retain", "Delete"], i),
    volumeBindingMode: pick(
      [
        "WaitForFirstConsumer",
        "Immediate",
        "WaitForFirstConsumer",
        "Immediate",
        "Immediate",
      ],
      i,
    ),
    allowVolumeExpansion: i % 2 === 0,
    isDefault: i === 0,
    createdAt: ago(AGES[i]),
  }),
  gateways: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    gatewayClassName: pick(
      ["envoy", "istio", "gke-l7-global-external-managed", "nginx", "cilium"],
      i,
    ),
    listeners: [],
    listenerSummary: rows(
      i === 2 ? 4 : 2,
      (l) => `${pick(["http", "https", "grpc", "tcp"], l)}:${80 + l * 363}`,
    ),
    listenerCount: 2,
    addresses:
      i === 2
        ? ["203.0.113.10", "gw-0123456789abcdef.elb.eu-central-1.amazonaws.com"]
        : ["10.0.0.5"],
    programmed: pick(["True", "False", "True", "Unknown", ""], i),
    accepted: "True",
    createdAt: ago(AGES[i]),
  }),
  httproutes: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    hostnames:
      i === 2
        ? [
            "payments-api.eu-central-1.internal.example-corp.com",
            "payments-admin.example-corp.com",
            "x.example.com",
          ]
        : ["app.example.com"],
    parentRefs: [],
    parentSummary:
      i === 2
        ? [
            "infra-gateways/public-external-gateway",
            "infra-gateways/internal",
            "other/gw",
          ]
        : ["default/web-gw"],
    ruleCount: i * 4,
    createdAt: ago(AGES[i]),
  }),
  gatewayclasses: (i) => ({
    name: pick(
      ["envoy", "istio", "gke-l7-global-external-managed", "nginx", "cilium"],
      i,
    ),
    controllerName: pick(
      [
        "gateway.envoyproxy.io/gatewayclass-controller",
        "istio.io/gateway-controller",
        "networking.gke.io/gateway",
        "k8s.nginx.org/nginx-gateway-controller",
        "io.cilium/gateway-controller",
      ],
      i,
    ),
    description:
      i === 2
        ? "Global external Application Load Balancer managed by GKE with Cloud Armor and CDN support enabled"
        : "Default class",
    accepted: pick(["True", "False", "True", "Unknown", ""], i),
    createdAt: ago(AGES[i]),
  }),
  referencegrants: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    from: rows(i === 2 ? 3 : 1, (f) => ({
      group: "gateway.networking.k8s.io",
      kind: pick(["HTTPRoute", "GRPCRoute", "TLSRoute"], f),
      namespace: LONG_NS,
    })),
    to: rows(i === 2 ? 3 : 1, (t) => ({
      group: "",
      kind: pick(["Secret", "Service"], t),
      name: t === 0 ? "wildcard-tls" : "",
    })),
    createdAt: ago(AGES[i]),
  }),
};

const generic: Record<string, (i: number) => Record<string, unknown>> = {
  endpoints: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    addressesCount: i * 3 + 1,
    ports:
      i === 2
        ? "8080/TCP, 8443/TCP, 9090/TCP, 9091/TCP, 15021/TCP, 50051/TCP"
        : "80/TCP",
    createdAt: ago(AGES[i]),
  }),
  configmaps: (i) => ({
    name: i === 2 ? "kube-root-ca.crt-and-extra-bundle-for-payments" : NAMES[i],
    namespace: NAMESPACES[i],
    dataCount: i * 12,
    createdAt: ago(AGES[i]),
  }),
  secrets: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    type: pick(
      [
        "Opaque",
        "kubernetes.io/service-account-token",
        "kubernetes.io/dockerconfigjson",
        "kubernetes.io/tls",
        "helm.sh/release.v1",
      ],
      i,
    ),
    dataCount: i + 1,
    createdAt: ago(AGES[i]),
  }),
  crds: (i) => ({
    name: pick(
      [
        "certificates.cert-manager.io",
        "virtualservices.networking.istio.io",
        "verticalpodautoscalercheckpoints.autoscaling.k8s.io",
        "applications.argoproj.io",
        "ec2nodeclasses.karpenter.k8s.aws",
      ],
      i,
    ),
    group: pick(
      [
        "cert-manager.io",
        "networking.istio.io",
        "autoscaling.k8s.io",
        "argoproj.io",
        "karpenter.k8s.aws",
      ],
      i,
    ),
    kind: pick(
      [
        "Certificate",
        "VirtualService",
        "VerticalPodAutoscalerCheckpoint",
        "Application",
        "EC2NodeClass",
      ],
      i,
    ),
    version: pick(["v1", "v1beta1", "v1alpha1", "v1", "v1beta1"], i),
    scope: i % 2 === 0 ? "Namespaced" : "Cluster",
    createdAt: ago(AGES[i]),
  }),
  serviceaccounts: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    secretsCount: i,
    createdAt: ago(AGES[i]),
  }),
  "k8s-roles": (i) => ({
    name:
      i === 2
        ? "system:controller:horizontal-pod-autoscaler-extended"
        : NAMES[i],
    namespace: NAMESPACES[i],
    rulesCount: i * 4 + 1,
    createdAt: ago(AGES[i]),
  }),
  "k8s-rolebindings": (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    roleKind: pick(
      ["ClusterRole", "Role", "ClusterRole", "Role", "ClusterRole"],
      i,
    ),
    roleName: pick(
      [
        "cluster-admin",
        "pod-reader",
        "system:controller:horizontal-pod-autoscaler",
        "edit",
        "view",
      ],
      i,
    ),
    subjectsCount: i + 1,
    createdAt: ago(AGES[i]),
  }),
};

function resourceOverrides(): StubOverride[] {
  const re = new RegExp(
    `^/api/v1/clusters/${C}/resources/(?:generic/)?([a-z0-9-]+)$`,
  );
  return [
    {
      method: "GET",
      path: re,
      body: (url: URL) => {
        const type = re.exec(url.pathname.replace(/\/+$/, ""))?.[1] ?? "";
        const make = named[type] ?? generic[type];
        return envelope(make ? rows(5, make) : []);
      },
    },
  ];
}

const mirrored: StubOverride[] = [
  {
    method: "GET",
    path: `/api/v1/clusters/${C}/ingress-classes`,
    body: {
      data: rows(4, (i) => ({
        name: pick(["nginx", "traefik", "alb", "gce-internal-regional"], i),
        controller: pick(
          [
            "k8s.io/ingress-nginx",
            "traefik.io/ingress-controller",
            "ingress.k8s.aws/alb",
            "networking.gke.io/ingress-gce-internal",
          ],
          i,
        ),
        parameters: null,
        labels: {},
        annotations: {},
        is_default: i === 0,
        last_seen_at: ago(AGES[i]),
        created_at: WIRE_NOW,
        updated_at: WIRE_NOW,
      })),
    },
  },
  {
    method: "GET",
    path: `/api/v1/clusters/${C}/gateway-classes`,
    body: {
      data: rows(4, (i) => ({
        name: pick(
          ["envoy", "istio", "gke-l7-global-external-managed", "cilium"],
          i,
        ),
        description: "",
        parameters: null,
        labels: {},
        annotations: {},
        controller_name: pick(
          [
            "gateway.envoyproxy.io/gatewayclass-controller",
            "istio.io/gateway-controller",
            "networking.gke.io/gateway",
            "io.cilium/gateway-controller",
          ],
          i,
        ),
        accepted_status: pick(["True", "False", "Unknown", ""], i),
        last_seen_at: ago(AGES[i]),
        created_at: WIRE_NOW,
        updated_at: WIRE_NOW,
      })),
    },
  },
  {
    method: "GET",
    path: `/api/v1/clusters/${C}/network-policies`,
    body: {
      data: rows(4, (i) => ({
        namespace: NAMESPACES[i],
        name:
          i === 2
            ? "allow-ingress-from-payments-gateway-and-monitoring"
            : NAMES[i],
        labels: {},
        annotations: {},
        pod_selector: {},
        policy_types: i % 2 === 0 ? ["Ingress", "Egress"] : ["Ingress"],
        ingress_rules: [],
        egress_rules: [],
        is_managed: i === 1,
        last_seen_at: ago(AGES[i]),
        created_at: WIRE_NOW,
        updated_at: WIRE_NOW,
      })),
    },
  },
  {
    method: "GET",
    path: `/api/v1/clusters/${C}/resource-quotas`,
    body: { data: [] },
  },
  {
    method: "GET",
    path: `/api/v1/clusters/${C}/limit-ranges`,
    body: {
      data: [
        {
          namespace: LONG_NS,
          name: "default-limits",
          labels: {},
          annotations: {},
          limits: [
            {
              type: "Container",
              default: {
                cpu: "500m",
                memory: "512Mi",
                "ephemeral-storage": "2Gi",
              },
              defaultRequest: { cpu: "100m", memory: "128Mi" },
              max: { cpu: "4", memory: "8Gi" },
              min: { cpu: "50m", memory: "64Mi" },
            },
            {
              type: "PersistentVolumeClaim",
              min: { storage: "1Gi" },
              max: { storage: "100Gi" },
            },
            { type: "Pod", max: { cpu: "8", memory: "16Gi" } },
          ],
          last_seen_at: WIRE_NOW,
          created_at: WIRE_NOW,
          updated_at: WIRE_NOW,
        },
      ],
    },
  },
];

const gatekeeper: StubOverride = {
  method: "GET",
  path: `/api/v1/clusters/${C}/gatekeeper/constraints`,
  body: {
    data: {
      bundle: rows(2, (i) => ({
        name: pick(
          ["pods-must-have-resource-limits", "deny-privileged-containers"],
          i,
        ),
        kind: "K8sContainerLimits",
        api_version: "constraints.gatekeeper.sh/v1beta1",
        enforcement_action: pick(["deny", "dryrun"], i),
        violation_count: i * 12,
      })),
      custom: rows(3, (i) => ({
        name: pick(
          [
            "require-team-label-on-all-namespaces",
            "restrict-image-registries-to-internal",
            "no-latest",
          ],
          i,
        ),
        kind: pick(
          ["K8sRequiredLabels", "K8sAllowedRepos", "K8sDisallowedTags"],
          i,
        ),
        api_version: "constraints.gatekeeper.sh/v1beta1",
        enforcement_action: pick(["deny", "warn", "dryrun"], i),
        violation_count: i * 1000,
        desired_state: i === 2 ? "absent" : "present",
        sync_status: pick(["synced", "failed", "pending"], i),
        last_error:
          i === 1
            ? "admission webhook denied the request: template constraints.gatekeeper.sh not found in cluster"
            : "",
        yaml: "",
      })),
    },
  },
};

const crdDiscovery: StubOverride = {
  method: "GET",
  path: `/api/v1/clusters/${C}/resources/discovery`,
  body: {
    data: {
      cluster_id: C,
      resources: [],
      crds: rows(5, (i) => ({
        name: pick(
          [
            "certificates.cert-manager.io",
            "virtualservices.networking.istio.io",
            "verticalpodautoscalercheckpoints.autoscaling.k8s.io",
            "applications.argoproj.io",
            "ec2nodeclasses.karpenter.k8s.aws",
          ],
          i,
        ),
        group: pick(
          [
            "cert-manager.io",
            "networking.istio.io",
            "autoscaling.k8s.io",
            "argoproj.io",
            "karpenter.k8s.aws",
          ],
          i,
        ),
        kind: pick(
          [
            "Certificate",
            "VirtualService",
            "VerticalPodAutoscalerCheckpoint",
            "Application",
            "EC2NodeClass",
          ],
          i,
        ),
        plural: pick(
          [
            "certificates",
            "virtualservices",
            "verticalpodautoscalercheckpoints",
            "applications",
            "ec2nodeclasses",
          ],
          i,
        ),
        scope: i % 2 === 0 ? "Namespaced" : "Cluster",
        versions: [
          { name: "v1", storage: true, printer_columns: [] },
          { name: "v1beta1", storage: false, printer_columns: [] },
          { name: "v1alpha1", storage: false, printer_columns: [] },
        ],
      })),
      crd_continue: "",
      partial: false,
      errors: {},
    },
  },
};

const crInstances: StubOverride = {
  method: "GET",
  path: /\/k8s\/apis\/cert-manager\.io\/v1\/(?:namespaces\/[^/]+\/)?certificates$/,
  body: {
    apiVersion: "cert-manager.io/v1",
    kind: "CertificateList",
    items: rows(5, (i) => ({
      metadata: {
        name:
          i === 2
            ? "wildcard-payments-eu-central-1-internal-example-corp-com-tls"
            : NAMES[i],
        namespace: NAMESPACES[i],
        creationTimestamp: ago(AGES[i]),
      },
    })),
  },
};

interface Case {
  name: string;
  url: string;
  overrides: StubOverride[];
  /** Headers of the columns that may exceed 3x content (the `grow` column). */
  grow: string[];
  /** Expected number of tables with rows (mirrored page has several). */
  tables?: number;
  /** Header that must be rendered, proving the table loaded. */
  header: string;
}

const list = (type: string) => `/dashboard/clusters/${C}/${type}`;
const res = resourceOverrides();

const cases: Case[] = [
  {
    name: "Services",
    url: list("services"),
    overrides: res,
    grow: ["Name"],
    header: "Cluster IP",
  },
  {
    name: "Ingresses",
    url: list("ingresses"),
    overrides: res,
    grow: ["Name"],
    header: "Hosts",
  },
  {
    name: "NetworkPolicies",
    url: list("networkpolicies"),
    overrides: res,
    grow: ["Name"],
    header: "Policy Types",
  },
  {
    name: "PersistentVolumes",
    url: list("persistentvolumes"),
    overrides: res,
    grow: ["Name"],
    header: "Claim",
  },
  {
    name: "PersistentVolumeClaims",
    url: list("persistentvolumeclaims"),
    overrides: res,
    grow: ["Name"],
    header: "Volume",
  },
  {
    name: "StorageClasses",
    url: list("storageclasses"),
    overrides: res,
    grow: ["Name"],
    header: "Provisioner",
  },
  {
    name: "Gateways",
    url: list("gateways"),
    overrides: res,
    grow: ["Name"],
    header: "Listeners",
  },
  {
    name: "HTTPRoutes",
    url: list("httproutes"),
    overrides: res,
    grow: ["Name"],
    header: "Parent Gateways",
  },
  {
    name: "GatewayClasses",
    url: list("gatewayclasses"),
    overrides: res,
    grow: ["Name"],
    header: "Controller",
  },
  {
    name: "ReferenceGrants",
    url: list("referencegrants"),
    overrides: res,
    grow: ["Name"],
    header: "From",
  },
  {
    name: "Endpoints",
    url: list("endpoints"),
    overrides: res,
    grow: ["Name"],
    header: "Endpoints",
  },
  {
    name: "ConfigMaps",
    url: list("configmaps"),
    overrides: res,
    grow: ["Name"],
    header: "Data",
  },
  {
    name: "Secrets",
    url: list("secrets"),
    overrides: res,
    grow: ["Name"],
    header: "Type",
  },
  {
    name: "CRDs (generic)",
    url: list("crds"),
    overrides: res,
    grow: ["Name"],
    header: "Group",
  },
  {
    name: "ServiceAccounts",
    url: list("serviceaccounts"),
    overrides: res,
    grow: ["Name"],
    header: "Secrets",
  },
  {
    name: "Roles",
    url: list("k8s-roles"),
    overrides: res,
    grow: ["Name"],
    header: "Rules",
  },
  {
    name: "RoleBindings",
    url: list("k8s-rolebindings"),
    overrides: res,
    grow: ["Name"],
    header: "Subjects",
  },
  {
    name: "Mirrored resources",
    url: list("resources"),
    overrides: mirrored,
    grow: ["Name", "Default"],
    tables: 4,
    header: "Controller",
  },
  {
    name: "Gatekeeper constraints",
    url: list("gatekeeper"),
    overrides: [gatekeeper],
    grow: ["Name"],
    header: "Enforcement",
  },
  {
    name: "Custom resource definitions",
    url: list("custom-resources"),
    overrides: [crdDiscovery],
    grow: ["Kind"],
    header: "Plural",
  },
  {
    name: "Custom resource instances",
    url: list("custom-resources/cert-manager.io/v1/certificates"),
    overrides: [crdDiscovery, crInstances],
    grow: ["Name"],
    header: "Namespace",
  },
];

interface Issue {
  table: number;
  column: string;
  problem: string;
}

async function auditTables(page: Page, grow: string[]): Promise<Issue[]> {
  return page.evaluate((growHeaders) => {
    const issues: { table: number; column: string; problem: string }[] = [];
    const CELL = "td, [role='gridcell']";
    const rowsOf = (t: Element) =>
      [...t.querySelectorAll("tbody tr, [role='row']")].filter(
        (r) => r.querySelector(CELL) && !r.hasAttribute("data-subrow"),
      );
    const tables = [
      ...document.querySelectorAll("table, [role='grid']"),
    ].filter((t) => rowsOf(t).length > 0);
    const textWidth = (root: Element): number => {
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      let left = Infinity;
      let right = -Infinity;
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        if (!n.textContent?.trim()) continue;
        const range = document.createRange();
        range.selectNodeContents(n);
        for (const r of range.getClientRects()) {
          left = Math.min(left, r.left);
          right = Math.max(right, r.right);
        }
      }
      return right > left ? right - left : 0;
    };
    const clippedUnguarded = (cell: Element): string | null => {
      for (const el of [cell, ...cell.querySelectorAll("*")]) {
        if (el.closest("[data-cell-clip]")) continue;
        const style = getComputedStyle(el);
        const clips =
          style.overflow !== "visible" || style.textOverflow === "ellipsis";
        const hit =
          el.scrollWidth > el.clientWidth + 1 &&
          el.clientWidth > 4 &&
          (clips || el === cell);
        if (hit)
          return (el.textContent ?? "").trim().slice(0, 60) || el.tagName;
      }
      return null;
    };
    tables.forEach((table, ti) => {
      const ths = [
        ...table.querySelectorAll("thead th, [role='columnheader']"),
      ];
      const bodyRows = rowsOf(table);
      const scroller = table.parentElement;
      if (scroller && scroller.scrollWidth > scroller.clientWidth + 1) {
        issues.push({
          table: ti,
          column: "(table)",
          problem: "horizontal scroll at 1280px",
        });
      }
      ths.forEach((th, ci) => {
        const label = (th.textContent ?? "").trim();
        if (!label) return;
        const headerClip = clippedUnguarded(th);
        if (headerClip)
          issues.push({
            table: ti,
            column: label,
            problem: `header clipped: ${headerClip}`,
          });
        let content = textWidth(th) + 24;
        let padding = 0;
        for (const row of bodyRows) {
          const cell = [...row.children].filter((e) => e.matches(CELL))[ci];
          if (!cell) continue;
          const clip = clippedUnguarded(cell);
          if (clip)
            issues.push({
              table: ti,
              column: label,
              problem: `clipped without tooltip: ${clip}`,
            });
          const style = getComputedStyle(cell);
          padding =
            parseFloat(style.paddingLeft) + parseFloat(style.paddingRight);
          content = Math.max(content, textWidth(cell));
        }
        const width = th.getBoundingClientRect().width;
        if (!growHeaders.includes(label) && width > 3 * (content + padding)) {
          issues.push({
            table: ti,
            column: label,
            problem: `width ${Math.round(width)} > 3x content ${Math.round(content + padding)}`,
          });
        }
      });
    });
    if (tables.length === 0)
      issues.push({ table: -1, column: "-", problem: "no table rendered" });
    return issues;
  }, grow);
}

for (const c of cases) {
  test(`${c.name} columns fit at 1280px`, async ({ page, context }) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width: 1280, height: 800 });
    await installStubs(page, c.overrides);
    await seedAuth(context, page, adminStoreUser);
    await page.goto(c.url);
    await expect(
      page.getByRole("columnheader", { name: c.header }).first(),
    ).toBeVisible({
      timeout: 20_000,
    });
    // Mirrored page: expand every collapsed section.
    for (const toggle of await page
      .locator('button[aria-expanded="false"][aria-controls]')
      .all()) {
      await toggle.click();
    }
    // Rows load after the header renders; wait until real (non-skeleton) rows exist.
    await expect(
      page.locator("td, [role='gridcell']", { hasText: /\S/ }).first(),
    ).toBeVisible({ timeout: 40_000 });
    await page.waitForTimeout(500);
    const issues = await auditTables(page, c.grow);
    if (process.env.TABLE_SHOTS) {
      await page.screenshot({
        path: `${process.env.TABLE_SHOTS}/${c.name.replace(/[^a-zA-Z0-9]+/g, "-")}.png`,
        fullPage: true,
      });
    }
    expect(issues, JSON.stringify(issues, null, 1)).toEqual([]);
  });
}
