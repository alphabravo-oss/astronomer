# Dependency debt

This ledger records transitive modules that cannot be removed from this module
without replacing their maintained direct parent. Entries must name the parent,
prove whether the module is linked into the application, and carry a review
condition. It is not a waiver for reachable vulnerabilities.

## 2026-09-10

| Transitive module                            | Parent                                                                                                 | Runtime reachability                                          | Decision                                                                 | Review condition                                                          |
| -------------------------------------------- | ------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------- | ------------------------------------------------------------------------ | ------------------------------------------------------------------------- |
| `github.com/Azure/go-autorest/autorest/adal` | `github.com/golang-migrate/migrate/v4@v4.20.1`                                                         | `go mod why -m` reports that the main module does not need it | Accept the graph-only dependency; the migration parent is current        | Remove when `golang-migrate` drops the legacy Azure database driver graph |
| `github.com/aws/aws-sdk-go` v1               | `github.com/golang-migrate/migrate/v4@v4.20.1`; `github.com/google/certificate-transparency-go@v1.3.3` | `go mod why -m` reports that the main module does not need it | Accept the graph-only dependency; production AWS integrations use SDK v2 | Recheck on either parent upgrade                                          |
| `cloud.google.com/go/pubsub` v1              | `github.com/sigstore/rekor@v1.5.4`                                                                     | `go mod why -m` reports that the main module does not need it | Accept the graph-only dependency; Rekor is current                       | Remove when Rekor drops the v1 Pub/Sub graph                              |

Frontend upgrades are validated without forced peer dependencies. The
2026-09-10 compatible-major wave includes date-fns 4.4.0, Sonner 2.0.8,
Lucide 1.44.0 and js-yaml 5.4.1 (which supplies its own declarations).
Lucide's removed brand icons use neutral Git/provider symbols; js-yaml's
removed `noCompatMode` option is not carried into the new parser API.

The remaining major-version constraints were reproduced against registry
metadata and the actual TypeScript build:

- TypeScript is `6.0.3` because `typescript-eslint@8.70.0` declares
  `typescript <6.1.0`; move to TypeScript 7 when that parser supports it.
- ESLint is `9.39.5` because `eslint-plugin-jsx-a11y@6.10.2` declares support
  through ESLint 9; accessibility lint remains release-blocking until its next
  compatible release permits ESLint 10.

On 2026-09-11, the remaining application migrations landed: TanStack Table
`9.2.4`, React Pacer `0.23.0`, and the aligned wterm core/dom/react family
`0.5.0`. Pre-1.0 packages remain exact-pinned. DataTable uses v9's native
`useTable` with one explicitly composed feature graph, feature-indexed types,
`sortFn`, and reactive `table.state`; the former custom snapshot controller was
deleted. Virtualization uses manual pagination while retaining the same sorted
and filtered row model. No v8 adapter, peer override, or dual implementation is
retained. Existing terminal and pacing consumers use the current APIs directly.
Console tab bodies are dynamically loaded on first use, so the upgraded
terminal runtime is absent from the dashboard's initial static import closure;
tabs remain mounted across tab switches. React's native boolean `inert` prop
replaces the terminal container's former string/type-cast workaround.

Verification after the migration and Node 24 qualification: typecheck and
zero-warning ESLint pass; 205 frontend test files / 1,207 tests pass; the
production build and moderate dependency audit pass with zero vulnerabilities.
The unchanged eager-closure gate measures 371,809 gzip bytes for bootstrap,
379,526 for login, and 453,230 for the app (ceilings 418,816 / 428,032 /
489,472). Strict-peer installation, audit, and focused table/terminal/console
tests also pass under the declared Node 24.21.0 runtime.

The tooling blockers were retried, not inferred from application compile errors:

- `npm install --save-dev typescript@7.0.2 eslint@10 @eslint/js@10` failed
  with `ERESOLVE`. A separate TypeScript 7 install was accepted by npm 9 only
  with automatic peer-override warnings; `npm ls typescript` then failed with
  `ELSPROBLEMS`, marking TypeScript 7 invalid throughout the typed-lint graph.
  The supported TypeScript 6 version was restored immediately.
- `npm view typescript-eslint@latest version peerDependencies --json` returns
  `8.70.0` with `typescript: ">=4.8.4 <6.1.0"`. The only newer canary,
  `8.70.1-alpha.0`, has the same restriction.
- `npm view eslint-plugin-jsx-a11y@latest version peerDependencies --json`
  returns `6.10.2` with `eslint: "^3 || ^4 || ^5 || ^6 || ^7 || ^8 || ^9"`.
  Its published tags contain only `latest` and `v5-backport`; there is no
  ESLint 10-compatible release or prerelease.

Recheck these exact registry commands before changing the two retained tooling
majors. Accessibility and typed lint remain enabled; bypassing their peer
contracts is not an upgrade.

The supported Node 24 floor is `24.21.0`; `frontend/.nvmrc`, package engines,
the digest-pinned Docker builder, and active workflows agree. A lower patch
version is not a supported verification environment.

`govulncheck ./...` reported no reachable vulnerabilities when this ledger was
created. Any future reachable advisory fails the security gate regardless of
this record.
