# W07 finite collection inventory

This packet reconciles all 189 original handler candidates and 158 original OpenAPI candidates at source `02897f9c4ecf7d8f56a9652b3dff060e7b3d8c71`. It is a source inventory, not completion of W07 or proof that all authorization, counts, clients, cancellation, and costs are qualified.

The gzip files preserve the original JSON bytes. [packet.json](packet.json) binds compressed and decompressed hashes. `finite-reconciliation.json.gz` contains every original index, current module/function hashes, direct guard/call evidence, named canonical SQL ordering/limit/offset references, route dependency type/method bindings, and explicit family limitations. The original discovery inputs and the earlier 33-record manual trace are retained separately; their historical qualification text does not extend runtime acceptance.

All 158 OpenAPI candidates have a source classification: 150 connect to original candidate handlers through concrete GET registrations, six connect to omitted owners, and two raw Kubernetes patterns alias the same `ProxyHandler.HandleK8sProxy` registration. The omitted owners are Kubernetes resource discovery, service mesh inventory, Charlie command catalog/history/context search, and kubelet log projection. Internal helpers, mutation reads, detail children, fixed catalogs, SCIM, and external protocols remain visible in the inventory instead of becoming fictitious SQL list endpoints.

The 35 handler families include 83 shared legacy guards, 11 independent delivery offset lists, three signed opt-in collection handlers, two existing cluster cursor owners, and separate full-set, capped, aggregate, provider, and protocol delegates. Family assignments derive from bounded lexical extraction plus explicit reviewed classifications. Registration and direct-call evidence does not prove every indirect control, SQL count predicate, provider behavior, or browser consumer. The extractor does not resolve arbitrary Go call graphs or dynamic router code.

The next changes must respect the contracts this inventory distinguishes:

- The 83 existing `validateLegacyPage` sites reject unsupported deep offsets before the shared parser can clamp them. Internal fleet helpers inherit their caller's guard. Adding a global GET middleware would affect unrelated requests and is unnecessary.
- Delivery `parsePagination` genuinely supports offsets through `MaxInt32`, default limit 20 and maximum 200; Charlie findings/trigger events separately support offsets through 100,000. Their independent contracts must not receive the shared parser's 10,000 ceiling without an explicit compatible transition.
- Existing cluster cursors, SCIM `startIndex/count`, Charlie history cursors, Kubernetes `Continue/resourceVersion`, and log protocols keep their own owners. A collection-shaped response does not make them product SQL cursor endpoints.
- Full-set project repositories/catalogs and configuration collections preserve their released behavior until a bounded opt-in or truthful cap is characterized and paired with consumers. Returning `len(items)` establishes only the returned-set size where a reader has a cap.

Concrete remaining work has a finite owner rather than one implementation per candidate:

| Owner                                            | Source finding or qualification gap                                                                                                       | Required evidence/change                                                                                                                                                                                     |
| ------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `AdminQueuesHandler` / admin queue table         | DLQ requests Asynq's first 100 without `Page`; inspector failures produce zero summary fields.                                            | Characterize 101 tasks and inspection failure; preserve existing wire fields with an explicit page owner and truthful unknown status.                                                                        |
| `AdminTaskOutboxHandler` / admin operation table | SQL paging metadata exists, but the browser requests 100 and passes only the array into a local table.                                    | Characterize 101 rows, then use remote pagination, matching count/scope/filter, cancellation, and stable SQL ties.                                                                                           |
| `ServiceMeshHandler.Inventory`                   | Per-kind Kubernetes reader ignores `metadata.continue`; a successful first page yields a blank notice and subset total.                   | Characterize the omitted peer page, then finite continuation or explicit incomplete/unknown metadata without inventing missing resources.                                                                    |
| Catalog/control-plane signed readers             | Core traversal/RBAC fixtures are qualified; larger selective cached-plan cost is unresolved.                                              | Measure nine representative filters at 100,000 rows on actual prepared first/continuation SQL in auto/generic/custom modes. Include discarded rows and loops, then decide query/index changes from evidence. |
| Full-set/capped/aggregate families               | Domain/write caps, delegate completeness, stable tie ordering, matching counts, and actual browser use are not established for every row. | Trace shared readers and consumers; distinguish deliberate recent/top slices from false complete-estate claims. Preserve independent protocols and released full-set behavior.                               |

The four support-bundle continuation streams are qualified in the separate [support receipt](../supportbundle-after/README.md). That result does not qualify the service mesh reader or every Kubernetes collection delegate.

To reproduce source classification without compiling or mutating source:

```sh
python3 docs/assurance/technology-guide/w07/list-inventory/reconcile.py \
  --repo . \
  --inputs docs/assurance/technology-guide/w07/list-inventory \
  --output /tmp/w07-finite-reconciliation.json
```

The helper reads compressed inputs directly. A later integration commit changes the report's Git identity; compare exact source/function/SQL/route bindings against the declared freeze rather than treating a different HEAD as the same receipt. No new API behavior, source-policy acceptance, performance promise, or runtime test result is introduced by this packet.
