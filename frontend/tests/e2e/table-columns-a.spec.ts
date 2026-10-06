import { expect, test, type Page } from "@playwright/test";

import { seedAuth } from "./helpers/auth";
import { installStubs } from "../e2e-smoke/stubs";
import { adminStoreUser, SMOKE_CLUSTER_ID } from "../e2e-smoke/stub-overrides";
import type { StubOverride } from "../e2e-smoke/stub-overrides";

// Plan 031 P6b slice 1: clusters / nodes / workloads / pods / events tables
// render worst-case values at 1280px with no silently clipped cell, no
// clipped header, no horizontal scroll and no column wider than 3x its content
// (the single `grow` column is exempt).

const C = SMOKE_CLUSTER_ID;
const DAY = 86_400_000;
const ago = (days: number) => new Date(Date.now() - days * DAY).toISOString();
const AGES = [0.01, 3, 40, 330, 900];

const LONG_NODE = "ip-10-0-12-34.eu-central-1.compute.internal";
const LONG_NS = "cert-manager-webhook-system";
const LONG_POD = "payments-gateway-internal-grpc-7d9f8b6c54-x2k4m";
const LONG_IMAGE =
  "registry.k8s.io/ingress-nginx/controller:v1.11.2@sha256:d5f8217feeac4887cb1ed21f27c2674e58be06bd8f5184cacea2a69abaf78dce";
const NODES = ["node-a", "node-b", LONG_NODE, "gke-prod-pool-1-abc", "k3s-0"];
const NAMESPACES = [
  "default",
  "kube-system",
  LONG_NS,
  "payments",
  "monitoring",
];
const NAMES = ["web", "kube-dns", LONG_POD, "otel-collector-agent", "db-0"];

const pick = <T>(values: T[], i: number) => values[i % values.length];
const rows = <T>(count: number, make: (i: number) => T): T[] =>
  Array.from({ length: count }, (_, i) => make(i));

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

const get = (path: string | RegExp, body: unknown): StubOverride => ({
  method: "GET",
  path,
  body,
});

const clustersList = get("/api/v1/clusters", {
  ...envelope(
    rows(4, (i) => ({
      id: `c-${i}`,
      name: pick(
        ["prod-eu-central-1-primary", "smoke-east", "edge-k3s-0042", "dr"],
        i,
      ),
      display_name: pick(
        ["Production EU Central Primary", "Smoke East", "Edge 42", "DR"],
        i,
      ),
      status: pick(["active", "warning", "disconnected", "active"], i),
      provider: pick(["aws", "gcp", "bare_metal", "azure"], i),
      environment: "production",
      distribution: pick(["eks", "gke", "k3s", "aks"], i),
      kubernetes_version: pick(
        ["1.31", "v1.30.4+k3s1", "v1.29.8-gke.1", "1.28"],
        i,
      ),
      node_count: i * 40 + 3,
      pod_count: i * 1234 + 42,
      cpu_percentage: pick([25, 91, 7.5, 60], i),
      memory_percentage: pick([33, 78, 5, 99], i),
      last_heartbeat: ago(AGES[i]),
      created_at: ago(AGES[i]),
      updated_at: ago(AGES[i]),
      labels: {},
      annotations: {},
      agent_connected: true,
      is_local: false,
    })),
  ),
});

const nodesList = get(
  `/api/v1/clusters/${C}/nodes`,
  envelope(
    rows(5, (i) => ({
      name: NODES[i],
      status: pick(
        ["Ready", "NotReady", "SchedulingDisabled", "Ready", "Ready"],
        i,
      ),
      roles:
        i === 2 ? ["control-plane", "etcd", "worker", "master"] : ["worker"],
      kubernetesVersion: "v1.30.4+k3s1",
      os: "linux",
      architecture: "amd64",
      containerRuntime: "containerd://1.7.2",
      cpuCapacity: 16000,
      cpuUsage: 3200 + i * 2000,
      memoryCapacity: 64 * 1024 ** 3,
      memoryUsage: (8 + i * 11) * 1024 ** 3,
      podCapacity: 110,
      podCount: 17 + i * 20,
      conditions: [],
      createdAt: ago(AGES[i]),
    })),
  ),
);

const namespacesList = get(
  `/api/v1/clusters/${C}/namespaces`,
  envelope(
    rows(5, (i) => ({
      name: NAMESPACES[i],
      clusterId: C,
      status: i === 3 ? "Terminating" : "Active",
      podCount: i * 37,
      cpuUsage: 120 + i * 900,
      cpuLimit: i % 2 ? 4000 : 0,
      memoryUsage: (i + 1) * 300 * 1024 ** 2,
      memoryLimit: i % 2 ? 8 * 1024 ** 3 : 0,
      createdAt: ago(AGES[i]),
    })),
  ),
);

const podRow = (i: number) => ({
  name: NAMES[i],
  namespace: NAMESPACES[i],
  clusterId: C,
  phase: pick(["Running", "Pending", "Failed", "Running", "Succeeded"], i),
  status: pick(
    [
      "Running",
      "ContainerCreating",
      "CrashLoopBackOff",
      "Terminating",
      "Completed",
    ],
    i,
  ),
  ready: pick(["1/1", "0/2", "12/12", "2/3", "0/1"], i),
  restarts: pick([0, 3, 1204, 0, 17], i),
  lastRestartAt: i % 2 ? ago(AGES[i]) : undefined,
  node: NODES[i],
  ip: pick(
    ["10.42.0.17", "10.42.1.200", "fd00:10:244:1::2a4f", "10.0.0.5", ""],
    i,
  ),
  images: i === 2 ? [LONG_IMAGE, "ghcr.io/acme/sidecar:v2"] : [`nginx:1.2${i}`],
  containers: [],
  conditions: [],
  createdAt: ago(AGES[i]),
  age: pick(["5m", "3d", "40d", "330d", "2y"], i),
});

const podsList = get(`/api/v1/clusters/${C}/pods`, envelope(rows(5, podRow)));

const eventsList = get(
  `/api/v1/clusters/${C}/events`,
  envelope(
    rows(5, (i) => ({
      id: `ev-${i}`,
      type: i % 2 ? "Warning" : "Normal",
      reason: pick(
        ["Scheduled", "BackOff", "FailedScheduling", "Pulled", "Unhealthy"],
        i,
      ),
      message:
        i === 2
          ? "0/12 nodes are available: 3 node(s) had untolerated taint {node.kubernetes.io/unreachable: }, 9 Insufficient memory. preemption: 0/12 nodes are available."
          : "Successfully assigned default/web to node-a",
      involvedObject: {
        kind: pick(
          ["Pod", "Deployment", "Node", "Pod", "PersistentVolumeClaim"],
          i,
        ),
        name: i === 2 ? LONG_POD : NAMES[i],
        namespace: NAMESPACES[i],
      },
      count: pick([1, 4, 1204, 2, 17], i),
      firstTimestamp: ago(AGES[i] + 1),
      lastTimestamp: ago(AGES[i]),
    })),
  ),
);

const workloadsList = get(
  `/api/v1/clusters/${C}/workloads`,
  envelope(
    rows(5, (i) => ({
      name:
        i === 2
          ? "payments-gateway-internal-grpc"
          : pick(["web", "kube-dns", "x", "otel-agent", "db"], i),
      namespace: NAMESPACES[i],
      kind: pick(
        ["Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob"],
        i,
      ),
      clusterId: C,
      status: pick(["Running", "Pending", "Failed", "Running", "Succeeded"], i),
      ready: pick(["3/3", "0/1", "12/12", "1/2", "0/0"], i),
      replicas: 3,
      desiredReplicas: 3,
      images:
        i === 2 ? [LONG_IMAGE, "ghcr.io/acme/sidecar:v2"] : [`nginx:1.2${i}`],
      labels: {},
      annotations: {},
      createdAt: ago(AGES[i]),
      age: pick(["5m", "3d", "40d", "330d", "2y"], i),
    })),
  ),
);

const nodeDetail = get(`/api/v1/clusters/${C}/nodes/${LONG_NODE}`, {
  data: {
    name: LONG_NODE,
    status: "Ready",
    roles: ["worker"],
    labels: {},
    annotations: {},
    createdAt: ago(40),
    nodeInfo: {},
    cpuCapacity: 16000,
    cpuUsage: 3200,
    memoryCapacity: 64 * 1024 ** 3,
    memoryUsage: 8 * 1024 ** 3,
    podCapacity: 110,
    podCount: 3,
    addresses: [],
    unschedulable: false,
    conditions: rows(5, (i) => ({
      type: pick(
        [
          "Ready",
          "MemoryPressure",
          "DiskPressure",
          "PIDPressure",
          "NetworkUnavailable",
        ],
        i,
      ),
      status: i === 0 ? "True" : "False",
      reason: pick(
        [
          "KubeletReady",
          "KubeletHasSufficientMemory",
          "KubeletHasNoDiskPressure",
          "KubeletHasSufficientPID",
          "CalicoIsUp",
        ],
        i,
      ),
      message:
        "kubelet is posting ready status. AppArmor enabled and a very long explanatory message that wraps",
      lastHeartbeatTime: ago(0.01),
      lastTransitionTime: ago(AGES[i]),
    })),
    taints: rows(3, (i) => ({
      key: pick(
        [
          "node.kubernetes.io/unreachable",
          "dedicated",
          "node-role.kubernetes.io/control-plane",
        ],
        i,
      ),
      value: i === 1 ? "gpu-workloads" : "",
      effect: pick(["NoSchedule", "NoExecute", "PreferNoSchedule"], i),
    })),
    images: rows(4, (i) => ({
      name: i === 2 ? LONG_IMAGE : `docker.io/library/nginx:1.2${i}`,
      sizeBytes: (i + 1) * 70 * 1024 ** 2,
    })),
    pods: rows(5, (i) => ({
      name: NAMES[i],
      namespace: NAMESPACES[i],
      status: pick(
        ["Running", "Pending", "CrashLoopBackOff", "Running", "Completed"],
        i,
      ),
      ready: "1/1",
      restarts: pick([0, 3, 1204, 0, 17], i),
      createdAt: ago(AGES[i]),
      images:
        i === 2 ? [LONG_IMAGE, "ghcr.io/acme/sidecar:v2"] : [`nginx:1.2${i}`],
    })),
    events: rows(4, (i) => ({
      type: i % 2 ? "Warning" : "Normal",
      reason: pick(
        ["NodeReady", "NodeNotReady", "ContainerGCFailed", "Rebooted"],
        i,
      ),
      message:
        "Node condition changed; the kubelet reported a transition that needs operator attention soon",
      count: pick([1, 4, 1204, 2], i),
      firstTimestamp: ago(AGES[i] + 1),
      lastTimestamp: ago(AGES[i]),
    })),
  },
});

const genericRe = new RegExp(
  `^/api/v1/clusters/${C}/resources/generic/([a-z0-9-]+)$`,
);
const generics: Record<string, (i: number) => Record<string, unknown>> = {
  jobs: (i) => ({
    name: i === 2 ? "database-backup-nightly-28765432-abcd1" : NAMES[i],
    namespace: NAMESPACES[i],
    status: pick(["Complete", "Running", "Failed", "Complete", "Pending"], i),
    succeeded: i,
    completions: 5,
    createdAt: ago(AGES[i]),
  }),
  cronjobs: (i) => ({
    name: i === 2 ? "database-backup-nightly-retention-policy" : NAMES[i],
    namespace: NAMESPACES[i],
    schedule: pick(
      ["*/5 * * * *", "0 */6 * * *", "30 2 * * 1-5", "@daily", "0 0 1 1 *"],
      i,
    ),
    status: pick(["Active", "Suspended", "Active", "Active", "Active"], i),
    lastSchedule: ago(AGES[i]),
    activeCount: i,
    createdAt: ago(AGES[i]),
  }),
  replicasets: (i) => ({
    name: i === 2 ? "payments-gateway-internal-grpc-7d9f8b6c54" : NAMES[i],
    namespace: NAMESPACES[i],
    desired: 3,
    ready: i,
    available: i,
    createdAt: ago(AGES[i]),
  }),
  hpa: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    targetKind: "Deployment",
    targetName: i === 2 ? "payments-gateway-internal-grpc" : "web",
    minReplicas: 2,
    maxReplicas: 20 + i * 100,
    currentReplicas: 4,
    createdAt: ago(AGES[i]),
  }),
  poddisruptionbudgets: (i) => ({
    name: NAMES[i],
    namespace: NAMESPACES[i],
    minAvailable: i % 2 ? "" : "50%",
    maxUnavailable: i % 2 ? "1" : "",
    currentHealthy: i,
    desiredHealthy: 3,
    createdAt: ago(AGES[i]),
  }),
};
const genericList = get(genericRe, null);
genericList.body = (url: URL) => {
  const type = genericRe.exec(url.pathname.replace(/\/+$/, ""))?.[1] ?? "";
  const make = generics[type];
  return envelope(make ? rows(5, make) : []);
};

interface Case {
  name: string;
  url: string;
  overrides: StubOverride[];
  /** Headers of the columns that may exceed 3x content (the `grow` column). */
  grow: string[];
  /** Header that must be rendered, proving the table loaded. */
  header: string;
}

const list = (type: string) => `/dashboard/clusters/${C}/${type}`;
const node = (tab: string) =>
  `/dashboard/clusters/${C}/nodes/${LONG_NODE}?tab=${tab}`;
const common = [genericList];

const cases: Case[] = [
  {
    name: "Clusters",
    url: "/dashboard/clusters",
    overrides: [clustersList],
    grow: ["Name"],
    header: "Seen",
  },
  {
    name: "Nodes",
    url: list("nodes"),
    overrides: [nodesList],
    grow: ["Name"],
    header: "Roles",
  },
  {
    name: "Namespaces",
    url: list("namespaces"),
    overrides: [namespacesList],
    grow: ["Name"],
    header: "CPU Usage",
  },
  {
    name: "Pods",
    url: list("pods"),
    overrides: [podsList],
    grow: ["Name"],
    header: "Restarts",
  },
  {
    name: "Events",
    url: list("events"),
    overrides: [eventsList],
    grow: ["Message"],
    header: "Object",
  },
  {
    name: "Workloads",
    url: list("workloads"),
    overrides: [workloadsList],
    grow: ["Name"],
    header: "Image",
  },
  {
    name: "Jobs",
    url: list("jobs"),
    overrides: common,
    grow: ["Name"],
    header: "Completions",
  },
  {
    name: "CronJobs",
    url: list("cronjobs"),
    overrides: common,
    grow: ["Name"],
    header: "Schedule",
  },
  {
    name: "ReplicaSets",
    url: list("replicasets"),
    overrides: common,
    grow: ["Name"],
    header: "Available",
  },
  {
    name: "HPAs",
    url: list("hpa"),
    overrides: common,
    grow: ["Name"],
    header: "Target",
  },
  {
    name: "PDBs",
    url: list("poddisruptionbudgets"),
    overrides: common,
    grow: ["Name"],
    header: "Healthy",
  },
  {
    name: "Node pods",
    url: node("pods"),
    overrides: [nodeDetail],
    grow: ["Name"],
    header: "Restarts",
  },
  {
    name: "Node conditions",
    url: node("conditions"),
    overrides: [nodeDetail],
    grow: ["Message"],
    header: "Reason",
  },
  {
    name: "Node images",
    url: node("images"),
    overrides: [nodeDetail],
    grow: ["Image"],
    header: "Size",
  },
  {
    name: "Node events",
    url: node("events"),
    overrides: [nodeDetail],
    grow: ["Message"],
    header: "Count",
  },
  {
    name: "Node taints",
    url: node("taints"),
    overrides: [nodeDetail],
    grow: ["Key"],
    header: "Effect",
  },
];

interface Issue {
  column: string;
  problem: string;
}

async function auditTables(page: Page, grow: string[]): Promise<Issue[]> {
  return page.evaluate((growHeaders) => {
    const issues: { column: string; problem: string }[] = [];
    const tables = [...document.querySelectorAll("table")].filter(
      (t) => t.querySelectorAll("tbody tr").length > 0,
    );
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
          el.clientWidth > 0 &&
          (clips || el === cell);
        if (hit)
          return (el.textContent ?? "").trim().slice(0, 60) || el.tagName;
      }
      return null;
    };
    tables.forEach((table) => {
      const ths = [...table.querySelectorAll("thead th")];
      const bodyRows = [...table.querySelectorAll("tbody tr")].filter(
        (r) => !r.hasAttribute("data-subrow"),
      );
      const scroller = table.parentElement;
      if (scroller && scroller.scrollWidth > scroller.clientWidth + 1) {
        issues.push({
          column: "(table)",
          problem: "horizontal overflow at 1280px",
        });
      }
      ths.forEach((th, ci) => {
        const label = (th.textContent ?? "").trim();
        if (!label || label === "Actions" || label === "Expand row") return;
        const headerClip = clippedUnguarded(th);
        if (headerClip)
          issues.push({
            column: label,
            problem: `header clipped: ${headerClip}`,
          });
        let content = textWidth(th) + 24;
        let padding = 0;
        for (const row of bodyRows) {
          const cell = row.children[ci];
          if (!cell) continue;
          const clip = clippedUnguarded(cell);
          if (clip)
            issues.push({
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
            column: label,
            problem: `width ${Math.round(width)} > 3x content ${Math.round(content + padding)}`,
          });
        }
      });
    });
    if (tables.length === 0)
      issues.push({ column: "-", problem: "no table rendered" });
    return issues;
  }, grow);
}

for (const c of cases) {
  test(`${c.name} columns fit at 1280px`, async ({ page, context }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await installStubs(page, c.overrides);
    await seedAuth(context, page, adminStoreUser);
    // seedAuth pins the namespace-scope list to empty; the Namespaces table
    // needs rows, so its route is registered after (last registered wins).
    if (c.name === "Namespaces") {
      await page.route("**/api/v1/clusters/*/namespaces/**", (route) =>
        route.fulfill({ json: namespacesList.body as object }),
      );
    }
    await page.goto(c.url);
    await expect(
      page.getByRole("columnheader", { name: c.header }).first(),
    ).toBeVisible({ timeout: 20_000 });
    await page.waitForTimeout(300);
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
