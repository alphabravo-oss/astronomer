# Phase 6b table inventory (read-only audit)

Scope: `frontend/src` consumers of the shared `DataTable` (91 files match the grep; 6 are DataTable internals under `components/ui/`; the remaining 85 consumer files plus 15 column-definition modules yield the **per-table entries below**). Paths are relative to `frontend/src`.

## Baseline facts that drive every symptom
- `DataTable` default `layout="fit"` -> `components/ui/table.tsx:20` applies `table-fixed max-w-full` + `overflow-x-hidden`. No table in `components/` or `routes/` passes `layout="scroll"` (only `components/charlie/settings/agent-tab.tsx:86` and `automation-tab.tsx:618` do, not in this inventory). Fixed layout + no `width` = **equal-width columns**; every cell is single-line by default (`wrap` opt-in), so long values clip with ellipsis/hidden overflow.
- `Column<T>` today: `width?: string`, `align`, `wrap`, `sortable`, `filterable`, `hideable`, `rowActions`. Only ~15 of ~450 columns declare a width.
- Ad-hoc fixes already in cells (`truncate max-w-[260px]`, `max-w-48`, `min-w-36`) fight the fixed layout: a `max-w` smaller than the equal share leaves dead space; larger gets clipped by the cell.
- Header `None` below = no `header` string (row-actions column via `rowActions`/inline actions).

Legend (recommendation column): **G** = grow (flex remainder, truncate+Tooltip); **min(n)** = `minSize` hint in ch; **R** = right-align + tabular-nums; **NW** = never wrap/truncate (fit-content); **COMP** = needs composite cell (two-line / status+reason); **CH(2)** = chips cap 2 + "+N"; **MC** = mono + middle-ellipsis + copy; **TT** = full value in Tooltip; **ts** = relative age with exact-timestamp tooltip; **pin** = fixed + pinned right.
Typical widths: pod name 30-63 ch (hash suffix), deployment 20-45, namespace 8-63 (usually 10-25), node name 15-63 (EKS `ip-10-0-12-34.eu-west-1.compute.internal` = 44), image ref 45-95 (`registry.k8s.io/ingress-nginx/controller:v1.11.2@sha256:...` 100+), cluster IP 7-15, k8s version 7-20 (`v1.30.4+k3s1`), age 2-6 (`3d4h`), CRD group 15-45, URL 25-80.

Default width cost at 1280px (~1180px content): 7 columns = ~168px each (about 24 ch at 12px mono ~ 7px/ch), 11 columns = ~107px (about 15 ch) => any name/image/URL/namespaced-name column clips, any count/age/badge column is 2-4x too wide.

---
## 1. Clusters / nodes

### T01 Clusters list - `routes/dashboard/clusters/-columns.tsx:19` (used by `routes/dashboard/clusters/-page.tsx:81`)
Fleet cluster list, fit, 12 cols, equal share ~98px. Grow: **Name**. Symptom: Name/display+id clipped, Status pill and Last Heartbeat header tight, CPU%/Mem% bar+label overflows, Nodes/Pods oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + subtitle (display name 8-30, id uuid-ish) | name | G, COMP (name+subtitle), TT |
| Status | none | status enum (+decommissioning) | status | NW |
| Provider | none | text 3-12 | text | min(10) |
| Distribution | none | badge 3-10 | badge | NW |
| K8s Version | none | version mono 7-20 | version | NW, min(12) |
| Nodes | none (center) | count | count | R |
| Pods | none (center) | count 1-4 digits | count | R |
| CPU% | none | percent bar + label | percent | R, fixed ~120px |
| Mem% | none | percent bar + label | percent | R, fixed ~120px |
| Last Heartbeat | none | relative age | age | NW, ts |
| (actions) | none | row menu | actions | pin |

### T02 Estate clusters table - `components/clusters/estate-clusters-table.tsx:19`
Compact estate view, 8 cols, only 5 widths set (Status 9rem, Version 8rem, Nodes/Pods 6rem). Grow: **Name**. Symptom: Name+Provider clip (name column is the equal remainder but CPU/Memory unset get same share as Name).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name link | name | G, TT |
| Status | 9rem | status | status | NW (keep) |
| Provider | none | two-line (provider + region) | text | COMP, min(14) |
| Version | 8rem | version mono | version | NW |
| Nodes | 6rem right | count | count | R |
| Pods | 6rem right | count | count | R |
| CPU | none | percent/quantity mono | percent | R, drop equal share |
| Memory | none | percent/quantity mono | percent | R |

### T03 Agents - `routes/dashboard/agents/-columns.tsx:10` (page `-page.tsx:93`)
Agent fleet, 9 cols, no widths. Grow: **Cluster**. Symptom: Capabilities chips wrap/clip, Compatibility text clipped, Agent (status+session subtitle) clipped, Kubernetes two-line clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | name + subtitle | name | G, COMP |
| Agent | none | status badge + sub-line | status | COMP (status+reason), min(14) |
| Version | none | version mono | version | NW |
| Compatibility | none | text/badge with tooltip message | badge | NW, min(14) |
| Capabilities | none | chips (n up to 6+) | badge | CH(2) |
| Kubernetes | none | two-line version/platform | version | COMP, min(14) |
| Last Heartbeat | none | relative age | age | NW, ts |
| Session | none | two-line id + time | id | MC, min(12) |
| (actions) | none | button | actions | pin |

### T04 Cluster metrics - nodes - `components/monitoring/cluster-metric-columns.tsx:7` (used `cluster-metrics-page.tsx:249`)
4 cols. Grow: **Node**. Symptom: node name (up to 44 ch) clipped; CPU/Memory bars share equal width.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Node | none | name link | name | G, TT |
| CPU | none | percent bar + quantity | percent | R, ~140px |
| Memory | none | percent bar + bytes | percent | R, ~140px |
| Pods | none (center) | count | count | R |

### T05 Cluster metrics - namespaces - `components/monitoring/cluster-metric-columns.tsx:93` (used `cluster-metrics-page.tsx:265`)
4 cols. Grow: **Namespace**. Symptom: oversized Pods; usage columns hold quantity+unit.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Namespace | none | name link (8-63) | name | G |
| Pods | none (center) | count | count | R |
| CPU Usage | none | quantity (`1.2 cores`/`340m`) | count | R |
| Memory Usage | none | bytes (`1.4 GiB`) | bytes | R |

### T06 Nodes list - `components/resources/resource-list-columns.tsx:24` (actions `resource-core-tables.tsx:136`)
8 cols. Grow: **Name**. Symptom: node name (44 ch) clipped at ~150px, Roles chips clipped, CPU/Memory bar columns wide.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G, TT |
| Status | none | status enum | status | NW |
| Roles | none | chips (control-plane, worker, etcd) | badge | CH(2) |
| CPU | none | percent bar | percent | R |
| Memory | none | percent bar | percent | R |
| Pods | none (center) | count `n/110` | count | R |
| Age | none | relative age | age | NW, ts |
| (actions) | none | cordon/drain menu | actions | pin |

### T07 Namespaces list - `components/resources/resource-list-columns.tsx:137` (actions `resource-core-tables.tsx:287`)
6 cols. Grow: **Name**. Symptom: Status header/centre badge fine; Created/Usage columns oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono (up to 63) | name | G |
| Status | none (center) | status | status | NW |
| Pods | none (center) | count | count | R |
| CPU Usage | none | quantity | count | R |
| Memory Usage | none | bytes | bytes | R |
| Created | none | relative age | age | NW, ts |
| (actions) | none | menu | actions | pin |

### T08 Node detail - taints - `routes/dashboard/clusters/$id/nodes/$nodeName/-node-taints-tab.tsx:6` (+ picker `:77`)
4 cols. Grow: **Key** (keys like `node.kubernetes.io/unreachable` up to ~60). Symptom: Key clipped, Effect badge oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Key | none | text mono (10-60) | name | G, TT |
| Value | none | text mono (0-30) | text | min(12) |
| Effect | none | badge (NoSchedule/PreferNoSchedule) | badge | NW |
| (remove) | none | button | actions | pin |

### T09 Node detail - pods - `routes/dashboard/clusters/$id/nodes/$nodeName/-node-workload-tabs.tsx:14`
8 cols. Grow: **Name**. Symptom: Name (63) and Image (80) both clip; Status centered badge, Ready/Restarts oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono (30-63) | name | G, TT |
| Namespace | none | namespace mono (8-63) | text | min(14), TT |
| Status | none (center) | status | status | NW |
| Ready | none (center) | count `1/2` | count | R |
| Restarts | none | count w/ colour | count | R |
| Image | none | image ref mono truncate max-w (45-95) | text | min(28), TT; image-ref cell (tag visible) |
| Age | none | relative age | age | NW |

### T10 Node detail - conditions - `-node-workload-tabs.tsx:81`
6 cols. Grow: **Message** (line-clamp-2, 40-200 ch). Symptom: Message squeezed to 1/6; Heartbeat/Transition headers clip (`Last Transition`).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Type | none | text enum (Ready, MemoryPressure) | text | min(14) |
| Status | none | status | status | NW |
| Reason | none | text 6-30 | text | min(16) |
| Message | none | free text 40-200 | text | G, wrap 2 |
| Last Heartbeat | none | relative | age | NW, short header + TT |
| Last Transition | none | relative | age | NW, short header + TT |

### T11 Node detail - images - `-node-workload-tabs.tsx:145`
2 cols. Grow: **Image** (50-100 ch). Symptom: image clipped by equal 50% only if table narrow; Size oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Image | none | image ref mono | text | G, image-ref cell |
| Size | none right | bytes (`142 MiB`) | bytes | R, NW |

### T12 Node detail - events - `-node-workload-tabs.tsx:168`
5 cols. Grow: **Message**. Symptom: Message clamped to 1/5; Count/Type oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Type | none | text Normal/Warning | status | NW |
| Reason | none | text 6-30 | text | min(14) |
| Message | none | free text 40-250 | text | G, wrap 2 |
| Count | none (center) | count | count | R |
| Last Seen | none | relative | age | NW, ts |

### T13 Control-plane snapshots - `components/clusters/control-plane-snapshots-page.tsx:57`
8 cols. Grow: **Snapshot** (id mono + subtitle). Symptom: snapshot id clipped, "etcd revision" header clipped at 98px, Size/Created/Completed oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Snapshot | none | id mono + subtitle (30-60) | id | G, MC, COMP |
| Status | none | status pill | status | NW |
| etcd revision | none right | version/int mono | count | R, header min width |
| Size | none right | bytes | bytes | R |
| Taken by | none | text (email 10-40) | text | min(16) |
| Created | none | timestamp | date | NW, ts |
| Completed | none | timestamp | date | NW, ts |
| (actions) | none right | button | actions | pin |

### T14 Snapshot schedules - `components/clusters/snapshot-tables.tsx:94`
6 cols (Actions 4.5rem). Grow: **Name**. Symptom: Namespaces chips wrap/clip; Cron mono clipped (`0 */6 * * *`); Enabled switch oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name | name | G |
| Cron | none | mono expr 9-20 | text | min(16) NW |
| Namespaces | none | chips | badge | CH(2) |
| Enabled | none | switch | select | NW, fixed 72px |
| Last run | none | relative | age | NW, ts |
| (actions) | 4.5rem right | buttons | actions | pin (keep) |

### T15 Snapshots - `components/clusters/snapshot-tables.tsx:220`
7 cols (Actions 7.5rem). Grow: **Name**. Symptom: name (`snap-<cluster>-20260930-xx`, 30-50 ch) clipped; "W / E" header cryptic.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name | name | G, TT |
| Source | none | text/badge (schedule name) | text | min(14), TT |
| Phase | none | status pill | status | NW |
| Started | none | relative | age | NW |
| Completed | none | relative | age | NW |
| W / E | none | count pair `0/1` | count | R, tooltip "Warnings / Errors" |
| (actions) | 7.5rem right | buttons | actions | pin |

### T16 Snapshot restore tracking - `components/clusters/snapshot-restore-tracking.tsx:47`
4 cols. Grow: **Restore**. Symptom: Source cluster/snapshot ids (36-char uuid) clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Restore | none | button/name | name | G |
| Source cluster | none | uuid (36) | id | MC, min(20) |
| Source snapshot | none | uuid/name | id | MC, min(20) |
| Status | none | status | status | NW |

### T17 Cluster network access - snapshots - `routes/dashboard/clusters/$id/network-access/index.tsx:118`
4 cols (3 widths). Grow: **Captured**. Low risk. Captured ISO timestamp mono (24 ch) fine at remainder.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Captured | none | ISO timestamp mono 24 | date | G, ts |
| Drift | 6rem | text yes/no | status | NW (keep) |
| Effective | 7rem right | count | count | R (keep) |
| Desired | 7rem right | count | count | R (keep) |

### T18 System components - `routes/dashboard/clusters/$id/delivery/system-components/-columns.tsx:17` (used `index.tsx:70`)
13 cols, no widths: **densest cluster table**. Grow: **Component**. Symptom: Requests/limits and Storage two-line text clipped, 4 phase badges + Owner pill each at ~90px, headers (`Requests / limits`, `Ready / desired`) clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Component | none | name link + subtitle | name | G, COMP |
| Category | none | text capitalised 5-15 | text | min(12) |
| Owner | none | pill | badge | NW |
| Managed by | none | text 5-12 | text | min(12) |
| Health | none | status badge | status | NW |
| Compatibility | none | status badge | status | NW |
| Update | none | status badge | status | NW |
| Ready / desired | none | count pair + sub | count | R, COMP |
| Requests / limits | none | quantity two-line | text | R, COMP, min(18) |
| Storage | none | storage class + size | text | COMP, min(16) |
| Namespace | none | namespace link | text | min(14), TT |
| Version | none | version max-w-40 truncate | version | NW, min(12) |
| Age | none | relative | age | NW |

### T19 System component - volumes - `.../system-components/$componentId/-columns.tsx:13` (used `-content.tsx:212`)
8 cols. Grow: **Claim**. Symptom: claim name + sub clipped; "Requested / capacity" header clipped; Expandable/Snapshots oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Claim | none | name link + sub | name | G, COMP |
| Phase | none | status badge | status | NW |
| Storage class | none | text two-line | text | min(14) |
| Requested / capacity | none right | quantity pair | bytes | R, NW, header tooltip |
| Access | none | text `ReadWriteOnce, ...` | text | min(18) |
| Expandable | none | yes/no | status | NW |
| Snapshots | none | count link | count | R |
| Age | none | relative | age | NW |

### T20 System component - resources - `-columns.tsx:120`
4 cols. Grow: **Observation** (free text 20-200). Symptom: Observation squeezed to 1/4; Resource name clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Resource | none | name link + sub | name | min(24), COMP |
| Kind | none | text 5-30 | text | min(14) |
| Health | none | status badge | status | NW |
| Observation | none | free text | text | G, wrap 2 |

### T21 Cluster templates - `routes/dashboard/cluster-templates/index.tsx:70`
6 cols. Grow: **Template**. Symptom: Description truncates at `max-w-[3xx]` inside equal cell; Clusters bound oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Template | none | name + icon | name | G |
| Description | none | free text 20-120 | text | min(24), wrap 2 |
| Environment | none | badge | badge | NW |
| Clusters bound | none center | count | count | R |
| Created by | none | two-line user/time | text | COMP, min(16) |
| (actions) | none right | buttons | actions | pin |

### T22 Cluster group members - `routes/dashboard/settings/cluster-groups/-membership.tsx:20`
2 cols. Grow: **Cluster**. ID uuid 36 ch -> MC.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | name + icon | name | G |
| Cluster ID | none | uuid mono 36 | id | MC, min(20) |

## 2. Workloads / pods / events

### T23 Workloads (cluster Resources > Workloads) - `components/resources/resource-list-columns.tsx:366` (page `resource-list-page.tsx:333`, route `routes/dashboard/clusters/$id/workloads/index.tsx:47` prepends a Kind column)
7 cols on the route (6 + Kind). Grow: **Name**. Symptom: Name (20-45) and Image (50-95) both clip; Ready/Age oversized. Image already uses `truncate max-w-[..]` inside the equal cell.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Kind (route only) | none | text enum | badge | NW |
| Name | none | name mono (20-45) | name | G, TT |
| Namespace | none | namespace mono | text | min(14), TT |
| Ready | none center | count `2/3` | count | R |
| Status | none | status | status | NW |
| Image | none | image ref mono (50-95; multi: first+N) | text | min(28), image-ref cell, TT |
| Age | none | relative (string) | age | NW |
| (actions) | none | menu | actions | pin |

### T24 Workloads (estate/project grouping) - `components/clusters/workloads-table.tsx:169`
5 cols, 4 widths already (Kind 9rem, Namespace 12rem, Status 10rem, Age 8rem). Grow: **Name** (only unsized column, correct by luck). Symptom: none major; Namespace 12rem clips >20 ch. Model for the others.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Kind | 9rem | icon + text | badge | NW |
| Name | none | name link | name | G |
| Namespace | 12rem | namespace | text | min(14) TT (keep) |
| Status | 10rem | status | status | NW |
| Age | 8rem | relative | age | NW |

### T25 Pods list - `components/resources/resource-list-columns.tsx:253` (actions `resource-core-tables.tsx:586`)
10 cols + actions: **second densest table in the app**. Grow: **Name**. Symptom: pod name (63), namespace, Images (80+), Node (44) all clip at ~100px; Pod IP, Ready, Restarts, Age oversized in absolute terms; "Last Restart" header wide.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono (40-63) | name | G, TT |
| Namespace | none | namespace mono (8-63) | text | min(14), TT |
| Status | none | status | status | NW |
| Images | none | image refs (first + "+N") (50-95) | text | min(26), image-ref cell |
| Ready | none center | count `1/1` | count | R |
| Restarts | none | count + colour | count | R |
| Last Restart | none | relative, nowrap | age | NW, ts |
| Pod IP | none | IPv4/6 mono 7-39 | text | NW, min(15), MC |
| Node | none | node name mono (15-44) | text | min(20), TT |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T26 Cluster events - `components/resources/resource-list-columns.tsx:192`
6 cols. Grow: **Message** (line-clamp-2, 40-250). Symptom: Message squeezed to ~1/6 (~190px) and Object (`pod/foo-abc-123`, 25-70) clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Type | none | text Normal/Warning | status | NW |
| Reason | none | text 6-30 | text | min(14) |
| Object | none | kind/name mono (25-70) | text | min(24), TT |
| Message | none | free text | text | G, wrap 2 |
| Count | none center | count | count | R |
| Last Seen | none | relative | age | NW |

### T27 Workload detail - pods - `components/resources/workload-resource-tabs.tsx:57`
6 cols. Grow: **Name**. Symptom: pod name (63) and node name clipped at 1/6.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name link | name | G, TT |
| Status | none | status | status | NW |
| Ready | none | count mono | count | R |
| Restarts | none | count | count | R |
| Node | none | node name mono | text | min(20), TT |
| Age | none | relative | age | NW |

### T28 Namespace detail - resources - `components/resources/namespace-detail-page.tsx:53`
5 cols. Grow: **Name**. Symptom: Details (free text) clipped; Kind oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Kind | none | text 3-25 | badge | min(12) |
| Name | none | name link mono | name | G, TT |
| Status | none | status | status | NW |
| Details | none | free text 10-80 | text | min(20), wrap 2 (secondary grow) |
| Age | none | relative | age | NW |

### T29 Jobs - `components/resources/resource-generic-columns.tsx:6`
5 cols. Grow: **Name** (job names with hash 30-63). Symptom: name clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace mono | text | min(14) |
| Status | none | status | status | NW |
| Completions | none center | count `1/1` | count | R |
| Age | none | relative | age | NW |

### T30 CronJobs - `resource-generic-columns.tsx:51`
7 cols. Grow: **Name**. Symptom: Schedule (`*/5 * * * *`, 9-20 mono) and "Last Schedule" header clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Schedule | none | cron mono | text | NW, min(16) |
| Status | none | status | status | NW |
| Last Schedule | none | relative | age | NW, ts |
| Active | none center | count | count | R |
| Age | none | relative | age | NW |

### T31 ReplicaSets - `resource-generic-columns.tsx:594`
6 cols. Grow: **Name** (hash names 30-63).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Desired | none center | count | count | R |
| Ready | none center | count | count | R |
| Available | none center | count | count | R |
| Age | none | relative | age | NW |

### T32 HorizontalPodAutoscalers - `resource-generic-columns.tsx:197`
6 cols. Grow: **Name**. Symptom: Target (`Deployment/checkout-api`, 20-60) clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Target | none | kind/name mono (20-60) | text | min(24), TT (secondary) |
| Min/Max | none center | count pair | count | R |
| Replicas | none center | count | count | R |
| Age | none | relative | age | NW |

### T33 PodDisruptionBudgets - `resource-generic-columns.tsx:314`
6 cols. Grow: **Name**. Symptom: headers `Min Available`/`Max Unavailable` clip at ~170px of 6; counts oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Min Available | none center | int or % | count | R, short header+TT |
| Max Unavailable | none center | int or % | count | R, short header+TT |
| Healthy | none center | count pair | count | R |
| Age | none | relative | age | NW |

### T34 ResourceQuotas and LimitRanges (generic, 2 tables) - `resource-generic-columns.tsx:254` and `:284`
Each 3 cols (Name, Namespace, Age). Grow: **Name**. Symptom: Age gets 33% of width.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Age | none | relative | age | NW |

## 3. Networking / storage / policy / RBAC resources

### T35 Services - `resource-list-columns.tsx:417` (actions `resource-network-tables.tsx:46`)
7 cols. Grow: **Name**. Symptom: Ports (`80/TCP,443/TCP,9090/TCP` up to 60) and Cluster IP clip; Type badge oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Type | none | badge (ClusterIP/NodePort/LoadBalancer) | badge | NW |
| Cluster IP | none | IPv4 mono 7-15 | text | NW, min(15), MC |
| Ports | none | port list 7-60 | text | min(20), CH(2) |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T36 Ingresses - `resource-list-columns.tsx:475` (actions `resource-network-tables.tsx:207`)
7 cols. Grow: **Hosts** (multi-host 20-120) or Name; choose **Name**, give Hosts minSize. Symptom: Hosts `truncate max-w-[..]` clip; TLS yes/no oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Class | none | text 3-20 | text | min(12) |
| Hosts | none | hostnames mono (20-120) | text | min(26), CH(2), TT |
| TLS | none | yes/no | status | NW |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T37 NetworkPolicies - `resource-list-columns.tsx:539` (actions `resource-network-tables.tsx:371`)
7 cols. Grow: **Name**. Symptom: "Ingress Rules"/"Egress Rules" headers clip at ~120px; counts oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Policy Types | none | chips (Ingress/Egress) | badge | NW |
| Ingress Rules | none center | count | count | R, short header |
| Egress Rules | none center | count | count | R, short header |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T38 PersistentVolumes - `resource-list-columns.tsx:602` (actions `resource-storage-tables.tsx:51`)
8 cols. Grow: **Name** (`pvc-<uuid>` = 40 ch). Symptom: Name and Claim (`ns/pvc`, 20-90) clip; "Access Modes" header and `ReadWriteOnce` clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono (up to 40+) | name | G, TT |
| Status | none | status | status | NW |
| Capacity | none | quantity `10Gi` | bytes | R |
| Access Modes | none | text `RWO` list | text | min(14), abbreviate |
| Storage Class | none | text 5-30 | text | min(14) |
| Claim | none | namespaced-name mono (20-90) | text | min(24), TT |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T39 PersistentVolumeClaims - `resource-list-columns.tsx:665` (actions `resource-storage-tables.tsx:184`)
8 cols. Grow: **Name**. Symptom: Volume (`pvc-<uuid>` 40) clipped; Capacity/Status oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Status | none | status | status | NW |
| Capacity | none | quantity | bytes | R |
| Storage Class | none | text | text | min(14) |
| Volume | none | uuid-ish name mono 40 | id | MC, min(24) |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T40 StorageClasses - `resource-list-columns.tsx:727` (actions `resource-storage-tables.tsx:346`)
6 cols. Grow: **Name**. Symptom: Provisioner (`ebs.csi.aws.com`, `rancher.io/local-path`, 15-40) and "Reclaim Policy"/"Binding Mode" (`WaitForFirstConsumer` 20) clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + default badge | name | G |
| Provisioner | none | text mono 15-40 | text | min(26), TT |
| Reclaim Policy | none | text enum | text | min(12), short header |
| Binding Mode | none | text enum up to 20 | text | min(22) |
| Expansion | none | yes/no | status | NW |
| (actions) | none | menu | actions | pin |

### T41 Gateways - `resource-gateway-tables.tsx:68` (actions `:416`)
8 cols. Grow: **Name**. Symptom: Listeners/Addresses chips clip (`max-w` truncate), Class clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Class | none | text mono 5-30 | text | min(16) |
| Listeners | none | chips `http:80` | badge | CH(2) |
| Addresses | none | IP/host mono truncate | text | min(20), TT |
| Programmed | none center | status pill | status | NW |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T42 Gateway routes (HTTP/GRPC/TCP/...) - `resource-gateway-tables.tsx:153` (actions `:554`)
7 cols. Grow: **Name**. Symptom: Hostnames and Parent Gateways clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Parent Gateways | none | chips | badge | CH(2), short header |
| Hostnames | none | hostnames mono truncate | text | min(24), CH(2), TT |
| Rules | none center | count | count | R |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T43 GatewayClasses - `resource-gateway-tables.tsx:222` (actions `:737`)
6 cols. Grow: **Name** (Description is longest free text -> give it the secondary min). Symptom: Controller (`gateway.envoyproxy.io/gatewayclass-controller` 45) and Description clipped at 1/6.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Controller | none | domain path mono 20-50 | text | min(30), TT |
| Accepted | none center | status pill | status | NW |
| Description | none | free text truncate max-w-[260px] | text | min(24), wrap 2 |
| Age | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T44 ReferenceGrants - `resource-gateway-tables.tsx:274` (actions `:868`)
5 cols. Grow: **Name**. From/To chips (`Kind ns`) clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| From | none | chips | badge | CH(2) |
| To | none | chips | badge | CH(2) |
| Age | none | relative | age | NW |

### T45 Endpoints - `resource-generic-columns.tsx:546`
5 cols. Grow: **Name**. Ports (list) clips.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Endpoints | none center | count | count | R |
| Ports | none | port list 3-40 | text | min(16), CH(2) |
| Age | none | relative | age | NW |

### T46 ConfigMaps - `resource-generic-columns.tsx:112` and Secrets - `:150` (2 tables)
ConfigMaps 4 cols (Name, Namespace, Data, Age); Secrets 5 cols (+Type badge `kubernetes.io/service-account-token`, 20-40 ch). Grow: **Name**. Symptom: Data count gets 25% width; Secret Type badge clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Type (Secrets) | none | badge long (up to 40) | badge | min(28), TT |
| Data | none center | count | count | R |
| Age | none | relative | age | NW |

### T47 CustomResourceDefinitions (generic) - `resource-generic-columns.tsx:370`
6 cols. Grow: **Name** (`certificates.cert-manager.io` 30-60). Symptom: Name, Group (15-45) clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | domain-style name mono truncate (30-60) | name | G, TT |
| Group | none | api group mono 12-45 | text | min(24), TT |
| Kind | none | text 5-30 | text | min(14) |
| Version | none | `v1`/`v1beta1` | version | NW |
| Scope | none | badge | badge | NW |
| Age | none | relative | age | NW |

### T48 ServiceAccounts - `resource-generic-columns.tsx:423`, Roles/ClusterRoles - `:461`, RoleBindings - `:499` (3 tables)
SA 4 cols (Name, Namespace, Secrets, Age); Roles 4 (Name, Namespace, Rules, Age); RoleBindings 5 (Name, Namespace, Role `ClusterRole/cluster-admin` 15-50, Subjects count, Age). Grow: **Name**. Symptom: count columns oversized; RoleBinding Role clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Namespace | none | namespace | text | min(14) |
| Role (RB only) | none | kind/name mono | text | min(24), TT |
| Secrets / Rules / Subjects | none center | count | count | R |
| Age | none | relative | age | NW |

### T49 Mirrored Ingress classes - `routes/dashboard/clusters/$id/resources/index.tsx:149`
4 cols (Last seen 10rem). Grow: **Name**. Controller (`k8s.io/ingress-nginx` 20-40) clips.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name mono | name | G |
| Controller | none | domain path mono | text | min(26) |
| Default | none | pill | badge | NW |
| Last seen | 10rem | relative | age | NW (keep) |

### T50 Mirrored GatewayClasses - `.../resources/index.tsx:231`
4 cols (Accepted 9rem, Last seen 10rem). Grow: **Name**. Same pattern as T49.

### T51 Mirrored NetworkPolicies - `.../resources/index.tsx:286`
5 cols (Last seen 10rem). Grow: **Name**. Symptom: Namespace + Name (two unsized mono columns) clip; Types/Owner pills.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Namespace | none | namespace mono | text | min(14) |
| Name | none | name mono | name | G |
| Types | none | chips | badge | NW |
| Owner | none | pill | badge | NW |
| Last seen | 10rem | relative | age | NW |

### T52 LimitRange items - `.../resources/index.tsx:464`
5 cols (all mono `fmtMap` text, 10-60 ch). Grow: **Type**? No: none are names; make **Default** grow. Symptom: `cpu=500m, memory=512Mi` maps clipped at 1/5.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Type | none | text enum | text | min(12) |
| Default | none | key=value map | text | G, CH(2) |
| DefaultRequest | none | key=value map | text | min(22), header "Default req." |
| Min | none | key=value map | text | min(20) |
| Max | none | key=value map | text | min(20) |

### T53 Custom resource definitions (explorer) - `components/clusters/custom-resources-page.tsx:107` (+ `:197`)
6 cols. Grow: **Kind**. Group/Plural mono (15-45) clip; Versions list.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Kind | none | text 5-30 | name | G |
| Group | none | api group mono | text | min(24), TT |
| Plural | none | mono 6-30 | text | min(18) |
| Versions | none | mono list | version | min(12), CH(2) |
| Scope | none | badge | badge | NW |
| Age | none | relative | age | NW |

### T54 Custom resource instances - `components/clusters/custom-resource-list.tsx:138` (+ `:220`)
4 cols. Grow: **Name**. Namespace mono clip; Age oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name link | name | G |
| Namespace | none | namespace mono | text | min(14) |
| Age | none | relative | age | NW |
| Actions | none | menu | actions | pin |

### T55 Generic resource table base - `components/resources/generic-resource-table.tsx:146`
Wraps T29-T48 base columns and appends an actions column (header None) via `rowActions`. No own columns. Action column: kind actions, pin.

### T56 Gatekeeper constraints - `routes/dashboard/clusters/$id/gatekeeper/-page.tsx:100`
6 cols. Grow: **Name**. Symptom: Status (two-line desired/observed) clipped; Source/Enforcement badges oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + sub (kind) | name | G, COMP |
| Source | none | badge | badge | NW |
| Enforcement | none | badge | badge | NW |
| Violations | none center | count | count | R |
| Status | none | status + sub | status | COMP (status+reason), min(16) |
| (actions) | none | button | actions | pin |

## 4. Delivery / Flux

### T57 Delivery estate - `routes/dashboard/delivery/-page.tsx:131`
7 cols. Grow: **Cluster**. Symptom: Agent/Flux phase badges and Flux two-line cell clipped; Assignments `3/4` oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | name + sub | name | G, COMP |
| Environment | none | text capitalised | badge | NW |
| Role | none | text | text | min(12) |
| Agent | none | phase badge | status | NW |
| Flux | none | phase badge + sub | status | COMP, min(14) |
| Assignments | none | count pair | count | R |
| Last heartbeat | none | relative | age | NW, ts |

### T58 Bundles - `routes/dashboard/delivery/bundles/-page.tsx:62`
3 cols. Grow: **Bundle**. Stable ID (`<uuid>` 36) clipped at 1/3 only on narrow.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Bundle | none | name + icon | name | G |
| Stable ID | none | uuid `<code>` 36 | id | MC, min(20) |
| Updated | none | relative | age | NW |

### T59 Bundle versions - `routes/dashboard/delivery/bundles/$bundleId/-page.tsx:94`
6 cols. Grow: **Version**. Symptom: "Immutable revision" (sha/digest, max-w-56 truncate) and "Renderer / scope" clip; Verification two-line.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Version | none | version link + sub | version | G, COMP |
| Renderer / scope | none | `helm · cluster` 10-30 | text | min(18) |
| Immutable revision | none | digest/commit mono (40-71) | id | MC, min(22) |
| Verification | none | phase badge + sub | status | COMP, min(16) |
| State | none | phase badge | status | NW |
| Created | none | relative | age | NW |

### T60 Bundle version - precedence - `.../versions/$versionId/index.tsx:85`
4 cols. Grow: **Effective evidence** (multi-line prose, whitespace-normal max-w-2xl). Symptom: evidence squeezed to 1/4 though it is the only wide column; Order oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Order | none | int | count | R, fixed ~60px |
| Layer | none | text 5-25 | text | min(14) |
| Scope | none | text 5-30 | text | min(14) |
| Effective evidence | none | free text multi-line | text | G, wrap |

### T61 Configuration templates - `routes/dashboard/delivery/configuration-templates/-page.tsx:96`
5 cols. Grow: **Template**. Configuration summary `12 values · 2 patches` (20-30).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Template | none | name + sub | name | G, COMP |
| Renderer | none | text enum | text | min(12) |
| Configuration | none | summary 20-30 | text | min(22) |
| Updated | none | relative | age | NW |
| Actions | none | buttons | actions | pin |

### T62 Deployments - `routes/dashboard/delivery/deployments/-page.tsx:102`
6 cols. Grow: **Deployment**. Symptom: Cluster id `<code>` (36) clipped; Revision (`max-w-48 truncate`) clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Deployment | none | name + icon | name | G |
| Cluster | none | uuid `<code>` 36 | id | MC, min(20) |
| Phase | none | phase badge | status | NW |
| Revision | none | digest/commit mono + sub | id | MC, COMP, min(20) |
| Drift | none | yes/no/derived | status | NW |
| Last observed | none | relative | age | NW |

### T63 Deployment detail - events - `routes/dashboard/delivery/deployments/$deploymentId/-page.tsx:369`
6 cols. Grow: **Message** (max-w-xl wrap). Symptom: Message squeezed; Phase `Pending -> Applying` mono 20-30 clipped; Gen oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Observed | none | timestamp nowrap | date | NW, ts |
| Event | none | text enum 8-25 | text | min(16) |
| Phase | none | `from -> to` mono 15-35 | text | min(22) |
| Result | none | phase badge | status | NW |
| Gen | none | int | count | R |
| Message | none | free text | text | G, wrap 2 |

### T64 Deployment detail - conditions - `.../deployments/$deploymentId/-page.tsx:420`
5 cols. Grow: **Sanitized message**. Header `Sanitized message` is the widest label; Status badge oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Condition | none | text 5-25 | text | min(16) |
| Status | none | phase badge | status | NW |
| Reason | none | text 5-30 | text | min(16) |
| Sanitized message | none | free text | text | G, wrap 2, header "Message" |
| Last transition | none | relative | age | NW |

### T65 Override sets - `routes/dashboard/delivery/override-sets/-page.tsx:101`
6 cols. Grow: **Override**. "Configuration" summary 20-30; Precedence int oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Override | none | name + sub | name | G, COMP |
| Scope | none | text capitalised | badge | NW |
| Precedence | none | int | count | R |
| Configuration | none | summary | text | min(22) |
| Updated | none | relative | age | NW |
| Actions | none | buttons | actions | pin |

### T66 Rollouts - `routes/dashboard/delivery/rollouts/-page.tsx:97` (1 width)
6 cols. Grow: **Rollout**. Symptom: Desired version id `<code>` (36) clipped; Progress bar `min-w-36` inside equal cell.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Rollout | none | name + icon | name | G |
| State | none | phase badge | status | NW |
| Strategy | none | text enum 6-20 | text | min(14) |
| Progress | none | bar + `3/10` | percent | R, ~140px |
| Desired version | none | uuid `<code>` 36 | id | MC, min(20) |
| Updated | none | relative | age | NW |

### T67 Rollout detail - clusters - `.../rollouts/$rolloutId/-page.tsx:129`
6 cols. Grow: **Cluster** (uuid 36 `<code>`). Symptom: Cohort / order and Assignment text oversized; Attempt int oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | uuid `<code>` 36 | id | G, MC |
| Cohort / order | none | `canary / 2` | text | min(16) |
| State | none | phase badge | status | NW |
| Assignment | none | text enum | text | min(14) |
| Attempt | none | int | count | R |
| Updated | none | relative | age | NW |

### T68 Sources - `routes/dashboard/delivery/sources/-page.tsx:104`
7 cols. Grow: **Source**. Symptom: Authentication (`ssh key + credential`), Trust (colored text) clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Source | none | name + URL sub (url 30-90) | name | G, COMP |
| Kind | none | text enum | badge | min(12) |
| Authentication | none | text 10-30 | text | min(18) |
| Trust | none | text/badge | badge | min(14) |
| Status | none | phase badge | status | NW |
| Last checked | none | relative | age | NW |
| (actions) | none right | buttons | actions | pin |

### T69 Targets - `routes/dashboard/delivery/targets/-page.tsx:55`
6 cols. Grow: **Target**. Bundle version uuid (36) clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Target | none | name + icon | name | G |
| Bundle version | none | uuid `<code>` 36 | id | MC, min(20) |
| Placement | none | text/warn | text | min(18) |
| Approval | none | text 8-20 | text | min(14) |
| State | none | phase badge | status | NW |
| Updated | none | relative | age | NW |

### T70 Target preview (placement) - `.../targets/$targetId/-preview-panel.tsx:9`
3 cols. Grow: **Details** (free text). 
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | name + sub | name | min(22), COMP |
| Decision | none | phase badge | status | NW |
| Details | none | free text / capability list | text | G, wrap 2 |

## 5. Apps / catalog / tools

### T71 Installed charts - `routes/dashboard/catalog/-installed-tab.tsx:31`
9 cols + actions. **Dense**. Grow: **Release**. Symptom: Release/Cluster/Namespace all clip; "Chart version" badge, Rev oversized, `Date` header ambiguous.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Release | none | name mono (10-53) | name | G, TT |
| Chart version | none | version badge (`cert-manager-v1.15.3`, 10-35) | version | NW, min(14) |
| Cluster | none | cluster name | text | min(16) |
| Namespace | none | namespace mono | text | min(14) |
| Status | none | status | status | NW |
| Rev | none center | int | count | R |
| Source | none | tool slug/repo | text | min(14) |
| Date | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T72 Installed release - history - `components/catalog/installed-release-detail.tsx:106`
3 cols. Grow: **Description**. Revision int oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Revision | none | int | count | R |
| Status | none | text (not badge) | status | NW |
| Description | none | free text 10-100 | text | G |

### T73 Helm repositories - `routes/dashboard/catalog/-repositories-table.tsx:37`
7 cols. Grow: **Name**. URL (`https://charts.example.com/stable`, 30-90 mono `truncate max-w`) clipped; Charts count oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + icon | name | min(18) |
| URL | none | url mono (30-90) | text | G, MC, TT |
| Type | none | badge | badge | NW |
| Charts | none | count | count | R |
| Last Synced | none | relative | age | NW |
| Status | none | status / error title | status | COMP |
| (actions) | none | buttons | actions | pin |

### T74 Tools matrix - `routes/dashboard/tools/index.tsx:95` (dynamic: one column per tool, header = `tool.name`)
2 fixed + N tool columns (N 6-12). Grow: **Cluster**. Symptom: with N=10 each tool cell ~85px; status cell (`ToolStatusCell` badge + version) clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | name + icon | name | G |
| Environment | none | text capitalised | badge | NW |
| (tool.name x N) | none | status badge + version | status | NW, min(14), COMP |

### T75 Extension proxy table - `components/extensions/ExtTable.tsx:41` (dynamic)
Columns derived from extension-declared `fields` (label, type). All unsized. Recommend: map extension field types to kinds (text->text, number->count, timestamp->age/date, badge->badge, ref->name); first text field grows.

## 6. Monitoring / alerting / logging

### T76 Monitoring overview - `routes/dashboard/monitoring/index.tsx:47`
6 cols. Grow: **Cluster**. Symptom: CPU/Memory quantity text oversized; cluster name+sub clipped.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | name + sub | name | G, COMP |
| Status | none | status | status | NW |
| CPU | none | percent/quantity | percent | R |
| Memory | none | percent/bytes | percent | R |
| Pods | none center | count | count | R |
| (link) | none | button | actions | pin |

### T77 Anomaly baselines - `routes/dashboard/alerting/baselines/index.tsx:35`
9 cols, **dense numeric**. Grow: **Metric** (`node_cpu_utilization` 15-50). Symptom: Metric+Cluster mono clip; Mean/Stddev/Last Value/P95 oversized but need right-align.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Metric | none | metric name mono (15-50) | name | G, TT |
| Cluster | none | cluster id/name mono | text | min(18) |
| Samples | none | count | count | R |
| Mean | none | float mono | count | R |
| Stddev | none | float mono | count | R |
| Last Value | none | float mono | count | R, short header |
| P95 | none | float mono | count | R |
| Window | none | duration text | text | min(10) |
| Updated | none | relative | age | NW |

### T78 Notification channels - `routes/dashboard/alerting/-channels-tab.tsx:38`
5 cols + actions. Grow: **Channel**.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Channel | none | name + type icon | name | G |
| Type | none | badge | badge | NW |
| Status | none | status | status | NW |
| Created | none | relative | age | NW |
| (actions) | none | buttons | actions | pin |

### T79 Alert events - `routes/dashboard/alerting/-events-tab.tsx:58`
7 cols. Grow: **Message** (truncate max-w-[300px], 30-200). Symptom: Message clipped at 300px cap and by cell; Rule button clipped; Severity badge oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Severity | none | badge | badge | NW |
| Rule | none | name link (button) 10-40 | name | min(20), TT |
| Message | none | free text | text | G, wrap 2 |
| Cluster | none | cluster name | text | min(16) |
| Fired | none | relative | age | NW, ts |
| Status | none | status | status | NW |
| (actions) | none | buttons | actions | pin |

### T80 Alert inhibitions - `routes/dashboard/alerting/-inhibition-panel.tsx:60`
7 cols. Grow: **Name**. Symptom: matcher chips (`alertname=Foo`) in three columns each at 1/7 wrap/clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name | name | G |
| Source matchers | none | chips | badge | CH(2), min(22) |
| Target matchers | none | chips | badge | CH(2), min(22) |
| Equal labels | none | chips | badge | CH(2), min(16) |
| Status | none | status | status | NW |
| Updated | none | relative | age | NW |
| (actions) | none | buttons | actions | pin |

### T81 Alert rules - `routes/dashboard/alerting/-rules-tab.tsx:25`
7 cols. Grow: **Rule** (name + description sub).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Rule | none | name + sub | name | G, COMP |
| Type | none | badge | badge | NW |
| Severity | none | badge | badge | NW |
| Cluster | none | cluster name | text | min(16) |
| Status | none | status | status | NW |
| Active | none | count | count | R |
| (actions) | none | buttons | actions | pin |

### T82 Alert silences - `routes/dashboard/alerting/-silences-tab.tsx:9`
5 cols (no actions). Grow: **Reason**. Matchers chips clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Reason | none | free text 10-80 | text | G |
| Duration | none | duration `2h` | text | NW |
| Matchers | none | chips | badge | CH(2), min(24) |
| Creator | none | user/email 10-40 | text | min(18) |
| Expires | none | relative | age | NW |

### T83 Logging operations - `routes/dashboard/logging/-operations-tab.tsx:33`
7 cols. Grow: **Error** (truncated message 20-200). Symptom: Error clipped; two badge columns + Created + "Age / Updated" oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Target Type | none | badge | badge | NW |
| Operation | none | badge | badge | NW |
| Status | none | status | status | NW |
| Created | none | relative + tooltip | age | NW, ts |
| Age / Updated | none | relative | age | NW, header "Updated" |
| Error | none | free text | text | G, wrap 2 |
| (actions) | none | retry button | actions | pin |

### T84 Logging outputs - `routes/dashboard/logging/-outputs-tab.tsx:176`
7 cols. Grow: **Output**. Symptom: Output (name + endpoint sub) clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Output | none | name + type icon + sub | name | G, COMP |
| Type | none | badge | badge | NW |
| Cluster | none | cluster name | text | min(16) |
| Connection | none | status | status | NW |
| Enabled | none | switch | select | NW |
| Created | none | relative | age | NW |
| (actions) | none | buttons | actions | pin |

### T85 Logging pipelines - `routes/dashboard/logging/-pipelines-tab.tsx:72`
7 cols. Grow: **Pipeline**. Namespaces chips clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Pipeline | none | name link + sub | name | G, COMP |
| Cluster | none | cluster name | text | min(16) |
| Namespaces | none | chips | badge | CH(2) |
| Outputs | none center | count | count | R |
| Enabled | none | toggle | select | NW |
| Created | none | relative | age | NW |
| (actions) | none | delete | actions | pin |

## 7. Security / scans / registries

### T86 CIS scans - `components/security/cis-scans-tab.tsx:80`
9 cols. Grow: **Cluster**. Symptom: Pass/Fail/Warn/Skip four numeric columns at ~100px each all oversized, Cluster/Profile (`cis-1.8-k3s` 12-30) clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | cluster name | name | G |
| Profile | none | mono 10-30 | text | min(18) |
| Run At | none | timestamp | date | NW, ts |
| Status | none right | status | status | NW |
| Pass | none right | count | count | R |
| Fail | none right | count | count | R |
| Warn | none right | count | count | R |
| Skip | none right | count | count | R |
| (link) | none | button | actions | pin |

### T87 CIS scan findings - `routes/dashboard/security/scans/$scanId/index.tsx:339`
6 cols; **already has widths** (expand 32px, Test ID 120px, Severity 100px, Status 90px). Grow: **Description**; Remediation (line-clamp-1) is the other unsized, so both get equal share; make Remediation `min(30)` wrap 2. Model table.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| (expand) | 32px | chevron | select | keep |
| Test ID | 120px | id mono `1.2.3` | id | keep |
| Description | none | free text 30-200 | text | G, wrap 2 |
| Severity | 100px | badge | badge | keep |
| Status | 90px | badge | status | keep |
| Remediation | none | free text 40-300 | text | min(30), wrap 2 |

### T88 CVE report - `components/security/report-cves.tsx:62`
1 col rendering `CVECard` (single composite card column). Grow: it is the only column. No change beyond kind `text`.

### T89 Pod security policies per cluster - `routes/dashboard/security/-policies-tab.tsx:22`
8 cols. Grow: **Cluster**. Symptom: Enforce/Audit/Warn three badge columns (levels `restricted`/`baseline`) oversized; Template + Sync Status clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | cluster name | name | G |
| Template | none | name 8-30 | text | min(18) |
| Enforce | none | badge (privileged/baseline/restricted) | badge | NW |
| Audit | none | badge | badge | NW |
| Warn | none | badge | badge | NW |
| Sync Status | none | status | status | NW |
| Applied | none | relative | age | NW |
| (actions) | none | buttons | actions | pin |

### T90 Pod security templates - `routes/dashboard/security/-templates-tab.tsx:14`
6 cols. Grow: **Name**; Description (`truncate max-w-[2xx]`) secondary.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + icon | name | G |
| Enforce | none | badge | badge | NW |
| Audit | none | badge | badge | NW |
| Warn | none | badge | badge | NW |
| Description | none | free text | text | min(24), wrap 2 |
| (actions) | none | buttons | actions | pin |

### T91 Template picker - `routes/dashboard/security/-template-picker.tsx:27`
2 cols: Template (name, G) / Selection (button, actions pin). Registry tables: none use shared DataTable today (no registry/image-scan DataTable users found).

## 8. Settings / admin / audit / backups / projects / RBAC

### T92 Projects - `routes/dashboard/projects/index.tsx:70`
8 cols + actions. **Dense, variable**. Grow: **Project**. Symptom: Cluster (name list), Namespaces chips, Resource Quota (multi-metric) clip; Members count oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Project | none | name + icon | name | G |
| Description | none | free text truncate | text | min(24), wrap 2 |
| Cluster | none | cluster name(s) | text | min(16) |
| Namespaces | none | chips | badge | CH(2) |
| Members | none | count/avatars | count | R |
| Resource Quota | none | quantity summary (`cpu 8 / mem 16Gi`) | text | R, COMP, min(20) |
| Created | none | relative | age | NW |
| (actions) | none | buttons | actions | pin |

### T93 RBAC - access bindings - `routes/dashboard/rbac/-bindings-tab.tsx:69`
6 cols. Grow: **Subject** (email 15-40). Role/Applies to clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Subject | none | user/group name | name | G |
| Scope | none | badge | badge | NW |
| Role | none | text | text | min(18) |
| Applies to | none | text (cluster/ns/project) 10-50 | text | min(22) |
| Created | none | relative | age | NW |
| (actions) | none right | buttons | actions | pin |

### T94 RBAC - effective permissions - `routes/dashboard/rbac/-effective-tab.tsx:20`
6 cols. Grow: **Granted By** (summary text 20-100). Symptom: Resource/Verb mono, Scope Target clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Applies | none | badge | badge | NW |
| Resource | none | mono (`pods/log`, `*`) 3-40 | text | min(20) |
| Verb | none | mono 3-20 | text | min(12) |
| Risk | none | badge | badge | NW |
| Granted By | none | summary text | text | G, wrap 2 |
| Scope Target | none | summary text | text | min(22) |

### T95 RBAC - effective bindings - `-effective-tab.tsx:131`
4 cols. Grow: **Role**; Rules count right.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Role | none | name/id | name | G |
| Scope | none | badge | badge | NW |
| Target | none | text | text | min(22) |
| Rules | none center | count | count | R |

### T96 RBAC - native rules - `routes/dashboard/rbac/-native-rules-tab.tsx:59`
6 cols. Grow: **User UUID** is an id; grow **API group / resource**. Symptom: UUID (36) clips, Verbs list clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| User UUID | none | uuid 36 | id | MC, min(20) |
| Cluster scope | none | cluster id or "All clusters" | text | min(18) |
| Namespace scope | none | namespace or "All namespaces" | text | min(18) |
| API group / resource | none | `apps / deployments` 10-50 | text | G |
| Verbs | none | list `get, list, watch` 3-60 | text | CH(2), min(20) |
| Actions | none | button | actions | pin |

### T97 RBAC - roles - `routes/dashboard/rbac/-roles-tab.tsx:30` (used for global + project roles)
7 cols. Grow: **Role**. Symptom: Description wraps/clips; "CRD grants" oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Role | none | name + icon | name | G |
| Description | none | free text 10-120 | text | min(24), wrap 2 |
| Type | none | badge builtin/custom | badge | NW |
| Rules | none center | count | count | R |
| CRD grants | none | count/link | count | R |
| Created | none | relative | age | NW |
| (actions) | none | menu | actions | pin |

### T98 RBAC - users - `routes/dashboard/rbac/-users-tab.tsx:31`
7 cols. Grow: **User**. Symptom: Email (up to 40) clip; Global Roles chips; Status multi-badge.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| User | none | avatar + display name | name | G |
| Email | none | email 12-40 | text | min(26), TT |
| Provider | none | badge | badge | NW |
| Global Roles | none | chips | badge | CH(2) |
| Status | none | badges (active/MFA) | status | COMP, min(14) |
| Last Login | none | relative | age | NW |
| (actions) | none | buttons | actions | pin |

### T99 Remote pickers (4 tables) (backup storage / project / role / user) - `components/backups/remote-storage-picker.tsx:67`, `components/projects/remote-project-picker.tsx:64`, `components/rbac/remote-role-picker.tsx:64`, `components/rbac/remote-user-picker.tsx:50`
4 tables, each 2-3 cols (Name/Project/Role/User + [Bucket] + Select). Grow: first column; Bucket min(20); Select = button, kind `select`/actions, fixed ~80px, pin.

### T100 Audit log (global) - `routes/dashboard/audit/index.tsx:136`
6 cols. **Dense, variable**. Grow: **Action** (`max-w-64`). Symptom: Time `min-w-36` + Actor `max-w-48` + Action `max-w-64` + Target `max-w-56` caps total > equal shares => horizontal squeeze/clip; Result two-line.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Time | none | timestamp two-line | date | NW, ts |
| Actor | none | user/email 10-40 | text | min(22), TT |
| Action | none | verb string `cluster.update` 10-50 | name | G |
| Target | none | resource name/id 10-60 | text | min(22), TT |
| Scope | none | text chips | badge | min(16), CH(2) |
| Result | none | status + sub | status | COMP |

### T101 Shell sessions - `routes/dashboard/audit/shell-sessions/index.tsx:41`
6 cols. Grow: **Pod** (`ns/pod-name`, 20-100). Symptom: Pod namespaced-name clipped; User email clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | cluster name mono | text | min(16) |
| User | none | email mono 10-40 | text | min(22) |
| Pod | none | namespaced-name 20-100 | name | G, TT |
| Status | none | badge | status | NW |
| Commands | none center | count | count | R |
| Started | none | relative | age | NW |

### T102 Settings audit tab - `routes/dashboard/settings/general/-audit-tab.tsx:24`
6 cols. Grow: **Resource**. Timestamp mono (19-24) + Source IP (7-39).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Timestamp | none | timestamp mono 19-24 | date | NW |
| User | none | user/email | text | min(20) |
| Action | none | badge | badge | min(16) |
| Resource | none | text 10-60 | text | G |
| Status | none | status | status | NW |
| Source IP | none | IP mono 7-39 | text | NW, min(15), MC |

### T103 API tokens - `routes/dashboard/settings/general/-tokens-tab.tsx:14`
6 cols. Grow: **Name**. Prefix mono (8-12).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + sub | name | G |
| Prefix | none | token prefix mono 8-14 | id | NW, MC |
| Status | none | text | status | NW |
| Expires | none | relative/date | date | NW |
| Last Used | none | relative | age | NW |
| (actions) | none | button | actions | pin |

### T104 SCIM tokens - `routes/dashboard/settings/auth/scim-tokens/-page.tsx:27`
5 cols. Grow: **Name**. Token mono (prefix) NW.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + icon | name | G |
| Token | none | masked/prefix mono | id | NW, MC |
| Last used | none | relative | age | NW |
| Created | none | relative | age | NW |
| (actions) | none | button | actions | pin |

### T105 Auth connectors - `routes/dashboard/settings/auth/index.tsx:72`
5 cols. Grow: **Display Name**. Name (mono id) 5-30.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Type | none | icon + type | badge | min(14) |
| Name | none | id mono | id | min(16) |
| Display Name | none | text | name | G |
| Status | none | status | status | NW |
| (actions) | none center | menu | actions | pin |

### T106 Backup targets - `routes/dashboard/settings/backup/-page.tsx:179`
5 cols. Grow: **Name** (+ sub). Symptom: Schedule cron text and Last job clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + sub | name | G, COMP |
| Schedule | none | cron/text 9-30 | text | min(16) |
| Status | none | status | status | NW |
| Last job | none | relative + sub | age | COMP, min(14) |
| (actions) | none | buttons/readonly badge | actions | pin |

### T107 Backup drill results - `routes/dashboard/settings/backup/-page.tsx:648`
5 cols. Grow: **Error** (max-w-[260px], free text up to 200). Symptom: Error clipped at 260px cap inside 1/5; Started mono timestamp 19-24.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Started | none | timestamp mono | date | NW |
| Status | none | status | status | NW |
| Schema | none | version int/mono | version | NW |
| Duration | none right | duration mono | count | R |
| Error | none | free text | text | G, wrap 2 |

### T108 Compliance baseline diff - `routes/dashboard/settings/compliance/baselines/index.tsx:131`
3 cols. Grow: **Field**? Value columns use `break-all` (wrap). Grow: **Current**/**Target** equal; set Field min(22), others G/G (two growers acceptable as exception; pick Target).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Field | none | dotted path mono 10-60 | name | min(22), TT |
| Current | none | value mono break-all | text | min(24), wrap |
| Target | none | value mono break-all | text | G, wrap |

### T109 GitOps sources - `routes/dashboard/settings/gitops/index.tsx:33`
7 cols. Grow: **Source**. Symptom: Repo URL (truncate, 30-90) clipped; Mode/On delete/Enabled centered, oversized; Last sync status/err text clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Source | none | name + icon | name | min(18) |
| Repo | none | git url mono (30-90) | text | G, MC, TT |
| Mode | none center | uppercase enum | badge | NW |
| On delete | none center | uppercase enum | badge | NW |
| Last sync | none | status + relative / error | status | COMP, min(16) |
| Enabled | none center | status | status | NW |
| (actions) | none | buttons | actions | pin |

### T110 Group mappings - `routes/dashboard/settings/group-mappings/index.tsx:33`
7 cols. Grow: **Group** (IdP group DN/path 10-80 mono). Target/Role clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Connector | none | mono badge | badge | min(14) |
| Group | none | mono 10-80 | name | G, TT |
| Scope | none | badge | badge | NW |
| Role | none | text | text | min(16) |
| Target | none | text/muted | text | min(18) |
| Created | none | relative | age | NW |
| (actions) | none | buttons | actions | pin |

### T111 Queues - `routes/dashboard/settings/operations/index.tsx:278`
8 cols, 6 widths already (Pending 6rem, Active 6rem, Scheduled 7rem, Retry 6rem, Completed 7rem; DLQ, State unsized). Grow: **Name**. Symptom: DLQ and State share the remainder equally with Name (3-way split) - good model otherwise.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | queue name mono 5-30 | name | G |
| Pending | 6rem right | count | count | R (keep) |
| Active | 6rem right | count | count | R (keep) |
| Scheduled | 7rem right | count | count | R (keep) |
| Retry | 6rem right | count | count | R (keep) |
| DLQ | none | count | count | R, fixed 6rem |
| Completed | 7rem right | count | count | R (keep) |
| State | none | badge paused/active | status | NW |

### T112 Dead-letter queue - `operations/index.tsx:408`
6 cols (Retries 6rem). Grow: **Last error** (max-w-md truncate 30-300). Symptom: ID `text-[11px]` (36 uuid) and Task type clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Task type | none | mono 10-40 | text | min(22) |
| ID | none | uuid mono 36 | id | MC, min(20) |
| Retries | 6rem right | count | count | R |
| Last error | none | free text | text | G, wrap 2 |
| Failed at | none | relative | age | NW |
| Actions | none | buttons | actions | pin |

### T113 Task outbox - `operations/index.tsx:541`
7 cols (Attempts 7rem). Grow: **Last error**. Task type name+sub.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Task type | none | mono + sub | name | min(22), COMP |
| Status | none | badge | status | NW |
| Queue | none | mono 5-30 | text | min(14) |
| Attempts | 7rem right | `2/5` | count | R |
| Next attempt | none | relative | age | NW |
| Last error | none | free text | text | G, wrap 2 |
| Actions | none | button | actions | pin |

### T114 Quota plans - `routes/dashboard/settings/quotas/index.tsx:83`
5 cols. Grow: **Plan**. Worst utilization bar needs ~140px.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Plan | none | name + icon | name | G |
| Enforcement | none | badge | badge | NW |
| Flagged limits | none right | count | count | R |
| Worst utilization | none | percent bar | percent | R, ~140px |
| Project caps | none | summary `3 / 10` | count | R |

### T115 Quota offenders - `routes/dashboard/settings/quotas/usage/index.tsx:92`
3 cols. Grow: **Scope**. Worst cap = percent bar + metric.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Scope | none | namespaced scope name + sub | name | G, COMP |
| Plan | none | link | text | min(16) |
| Worst cap | none | percent bar | percent | R |

### T116 Read-audit policies - `routes/dashboard/settings/read-audit/index.tsx:96`
6 cols (Verbs 8rem, Sample 6rem). Grow: **Path pattern** (`/api/v1/clusters/*/secrets`, 15-80 mono). Name and Path both unsized: Name=min(18), Path=G.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | mono 5-30 | name | min(18) |
| Path pattern | none | path glob mono 15-80 | text | G, TT |
| Verbs | 8rem | text `GET,LIST` | text | keep |
| Sample | 6rem right | percent | percent | R (keep) |
| Enabled | none | toggle | select | NW |
| (actions) | none | button | actions | pin |

### T117 SIEM forwarders - `routes/dashboard/settings/siem/-page.tsx:78`
6 cols. Grow: **Name**. Event filters chips clip.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + sub | name | G, COMP |
| Transport | none | badge | badge | NW |
| Event filters | none | chips | badge | CH(2), min(22) |
| Status | none | status | status | NW |
| Updated | none | relative | age | NW |
| (actions) | none | buttons | actions | pin |

### T118 SMTP sent emails - `routes/dashboard/settings/smtp/index.tsx:274`
5 cols. Grow: **To** (email 12-50). Time mono timestamp.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Time | none | timestamp mono | date | NW |
| To | none | email | text | G, TT |
| Template | none | badge | badge | NW |
| Status | none | status | status | NW |
| Attempts | none right | count | count | R |

### T119 Webhook deliveries - `routes/dashboard/settings/webhooks/$id/index.tsx:261`
6 cols. Grow: **Event** (`cluster.status.changed` 10-40 mono badge). Time mono.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Time | none | timestamp mono | date | NW |
| Event | none | mono badge 10-40 | text | G |
| Status | none | status | status | NW |
| HTTP | none | status code int | count | R |
| Attempts | none right | count | count | R |
| (actions) | none | button | actions | pin |

### T120 Webhook subscriptions - `routes/dashboard/settings/webhooks/index.tsx:51`
5 cols. Grow: **URL** (30-120 mono, `truncate max-w`). Name min(18).
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Name | none | name + icon | name | min(18) |
| URL | none | url mono | text | G, MC, TT |
| Enabled | none center | toggle | select | NW |
| Updated | none | relative | age | NW |
| (actions) | none | button | actions | pin |

## 9. Other

### T121 Global search results - `routes/dashboard/search/-page.tsx:192`
6 cols. Grow: **Name**. Symptom: Cluster, Namespace, Name all competing; Type pill/Age oversized.
| Header | Width | Shape | kind | Rec |
|---|---|---|---|---|
| Cluster | none | cluster name + icon | text | min(16) |
| Namespace | none | namespace mono | text | min(14) |
| Name | none | resource name | name | G, TT |
| Type | none | badge (kind) | badge | NW |
| Age | none | relative tabular | age | NW |
| Status | none | status | status | NW |

### Internals (not tables; no entry)
`components/ui/data-table*.tsx` (6 files), `components/resources/explorer-data-table.tsx` and `server-resource-explorer-table.tsx` (wrappers that forward `columns` from T23-T58), `components/monitoring/cluster-metrics-page.tsx` (hosts T04/T05), `routes/dashboard/clusters/-page.tsx`, `agents/-page.tsx`, `system-components/index.tsx`, `$componentId/-content.tsx` (hosts T01/T03/T18-T20).


---
## Ranking: 25 densest / most affected tables (columns x content variability, fit layout, no widths)
| Rank | Table | Cols | Why it is worst |
|---|---|---|---|
| 1 | T25 Pods list | 11 | 11 equal cols (~98px); pod name, namespace, images, node all long; 5 tiny columns oversized |
| 2 | T18 System components | 13 | 13 cols (~85px), 3 phase badges + two-line quantity/storage cells |
| 3 | T71 Installed charts | 9 | release+cluster+namespace+chart version all variable |
| 4 | T01 Clusters list | 11 | name+sub, status, CPU/Mem bars, heartbeat each at ~98px |
| 5 | T03 Agents | 9 | chips, two-line cells, session id, compat text |
| 6 | T92 Projects | 8 | chips + composite quota + multi-cluster cell |
| 7 | T100 Audit log | 6 | per-cell `max-w` caps fight fixed layout; verb/target strings up to 60 |
| 8 | T77 Anomaly baselines | 9 | 4 float columns need right-align; metric name 15-50 |
| 9 | T38 PersistentVolumes | 8 | `pvc-<uuid>` name, claim `ns/name`, access modes |
| 10 | T23 Workloads | 8 | name + image ref (50-95) + namespace |
| 11 | T06 Nodes list | 8 | node name 44, roles chips, 2 bars |
| 12 | T57 Delivery estate | 7 | agent/flux badges + composite |
| 13 | T41 Gateways | 8 | chips + addresses + class |
| 14 | T109 GitOps sources | 7 | repo URL 30-90, composite last-sync |
| 15 | T83 Logging operations | 7 | long error free text vs 4 small columns |
| 16 | T86 CIS scans | 9 | 4 numeric cols oversized, cluster/profile clip |
| 17 | T09 Node pods | 7 | name + namespace + image |
| 18 | T36 Ingresses | 7 | multi-host list, TLS |
| 19 | T80 Alert inhibitions | 7 | 3 chip-matcher columns |
| 20 | T39 PersistentVolumeClaims | 8 | uuid volume, class, capacity |
| 21 | T35 Services | 7 | port lists, cluster IP |
| 22 | T98 RBAC users | 7 | email, chips, composite status |
| 23 | T26 Cluster events | 6 | long message vs object |
| 24 | T42 Gateway routes | 7 | hostnames + parent chips |
| 25 | T79 Alert events | 7 | message capped at 300px, rule link |

Next tier (not in the 25): T63 deployment events, T59 bundle versions, T37 NetworkPolicies, T40 StorageClasses, T62 deployments, T117 SIEM, T112 DLQ, T120 webhooks.
Tables that already set widths (patterns to copy, not to repeat): T24 workloads-table, T87 CIS findings, T111 queues, T116 read-audit, T17 network-access, T02 estate clusters.

## Summary: proposed `kind` counts (column rows in this document)
| kind | columns | notes |
|---|---|---|
| text | 169 | namespaces, descriptions, enums, summaries; most need `minSize` |
| name | 92 | one per table; the single `grow` candidate in ~85% of tables |
| count | 82 | right-align + tabular-nums (includes quantities and floats) |
| age | 82 | relative time, never truncate, exact tooltip |
| status | 76 | status pills + phase badges, never truncate |
| badge | 70 | enum pills and chip lists (chip lists need CH(2)) |
| actions | 55 | pin right, fixed |
| id | 19 | uuids, digests, tokens: mono + middle-ellipsis + copy |
| percent | 14 | bar + label, right-aligned, fixed ~120-140px |
| date | 11 | absolute timestamps |
| version | 10 | k8s/chart versions, never truncate |
| bytes | 7 | storage/memory with unit, right-aligned |
| select | 6 | switches / toggles / expand chevrons / pickers |
| **total** | **693** | 127 tables; ~85% of text columns carry long variable strings so `text`/`name` dominate |

Cross-cutting findings
- 15 explicit widths exist in the whole tree (T02, T14, T15, T17, T24, T49-T51 Last seen/Accepted, T87, T111-T113, T116); everything else is equal share.
- Namespaced-name / image-ref / URL / uuid columns (`name`/`id`/`text` with mono) account for the clipping; they appear in 90+ tables as the same trio Name + Namespace + Age (generic resource tables T29-T48 share three near-identical column definitions in `resource-generic-columns.tsx`, so fixing the Name/Namespace/Age kinds there covers ~20 tables in one edit).
- Shared definitions (edit once, many tables): `resource-generic-columns.tsx` (20 tables), `resource-list-columns.tsx` (13), `resource-gateway-tables.tsx` (4), delivery pages share `DeliveryPhaseBadge`, `<code>{uuid}</code>` id cells (T58 Bundles, T62 Deployments, T66 Rollouts, T67 rollout clusters, T69 Targets) -> one `id` kind fix.
- Five header labels will clip even after kinds unless shortened: `Requested / capacity`, `Sanitized message`, `Min Available`/`Max Unavailable`, `Requests / limits`, `Last Transition`/`Last Heartbeat`.
- Dynamic-column tables (T74 tools matrix, T75 ExtTable) need kind assignment at runtime, not by static list; the guard test must allowlist them or require the factory to pass a kind.
