# Rancher-relative adopted-cluster UX benchmark v1

This directory defines evidence; it does not contain a comparative pass.
Astronomer's independent browser and live-cluster suites prove that its own
workflows function, but cannot prove that those workflows are faster, clearer,
or easier to recover than Rancher.

## Pinned comparison subjects

The Rancher backend comparison subject is commit
`e870de073d56eec568516fae4bc5f76c5d409909`. Its image build pins Rancher
Dashboard `v2.15.0-alpha1`. The sibling `rancher/ui` checkout is the legacy
Ember UI and is not a valid substitute for that Dashboard release. Every run
must additionally record immutable image digests and Astronomer's exact source
commit.

The scope is day-2 work on clusters that already exist. Cluster provisioning,
machine/node-pool lifecycle, Rancher Fleet compatibility, and comparisons of
Fleet with Astronomer's Flux-native delivery are excluded.

## Automated measurement

Run both products on the same isolated host allocation against equivalent
disposable adopted-cluster fixtures. Start each task on the authenticated
product landing page. Use the same browser build, viewport, cache mode, user
permissions, object names, and injected failure. The success condition is
observed through an independent Kubernetes/API probe, never inferred from a UI
toast.

The Playwright instrumentation contract in
`frontend/tests/benchmark/instrumentation.ts` records trusted pointer and
keyboard activations, route transitions, errors, recovery actions, active time,
and wall time. A runner must also record network failures, trace and screenshot
locations, and independently detected duplicate effects. Retain both cold- and
warm-cache repetitions; do not combine active-operator time with controller
convergence time.

Mechanical qualification requires paired evidence for every task and both
products, zero duplicate effects, successful required recovery, and retained
traces. Suggested non-inferiority policy is: no Astronomer task failure; no
Astronomer task more than 20 percent worse in median active time or control
activations; and Astronomer at or better than Rancher on the task-set median.
The run report must state the selected policy before results are collected.

## Human-only evaluation

Automation encodes selector knowledge and therefore cannot measure
discoverability or clarity. Use neutral task cards from `tasks-v1.json` in a
counterbalanced crossover study with first-use operators. Record unassisted
completion, first-click correctness, help requests, recovery success, and the
task-specific five-point clarity/safety questions. Conduct think-aloud review
after timing so it does not distort completion time.

Overall parity remains `pending` unless both the automated paired run and the
human clarity study pass. A missing subject, skipped task, unretained trace, or
absent human study must never be converted into a pass.

## Validation

Validate the contract and task definition:

```bash
node scripts/validate-rancher-benchmark.mjs
node --test scripts/tests/rancher-benchmark-contract.test.mjs
```

Validate a retained evidence document without printing credentials:

```bash
node scripts/validate-rancher-benchmark.mjs --evidence /path/to/benchmark-v1.json
```

The evidence format intentionally rejects credential-like field names. Store
only sanitized artifact references; never store passwords, bearer tokens,
kubeconfigs, session cookies, or registration credentials in benchmark JSON.
