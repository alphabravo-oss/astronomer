# Integrated build checkpoint, 2026-10-11

Implementation is active on `implement/032-technology-guide`. The application
source base is `27102246b69ee4fbd3f7d5ed1a06119ef02d8fb0`; this checkpoint's
commit adds the HTTP guard repairs, catalog factory/banner corrections and
canonical route inventory refresh. Catalog tooling remains separately integrated
at `665d4deec75a2ced2f3d630bb5a23d4267110452`. The entire plan is not complete.

The subsequent minimal qualification-helper fixes are integrated through
`54763ab397fa97cd11f7c7c11b974f9adf377ef0`. Both tagged integration packages
now compile, and all 28 discovered Go commands pass production builds with
actual source stamps and matching VCS metadata. The [retained build record](../../docs/assurance/technology-guide/w01/integrated-build-54763ab3/README.md)
preserves both original compiler failures and the successful correction. This
does not execute the integration fixtures or build signed container images.

The next priority is owned-cluster validation of the integrated candidate.
The existing local Astronomer deployment still has the previous three immutable
application image references; a read-only check found server, worker and frontend
each at one ready replica out of one desired. No new deployment, PR, push, tag or
publication occurred in this checkpoint. Ready replicas do not prove all product
workflows.

The [HTTP guard packet](../../docs/assurance/technology-guide/w02/http-status-peer-redirect/README.md)
retains actual baseline failures, the first corrected fixture failure, and the
final 72 passing race results across 31 parents with no skips. All eleven corrected
Go source bodies are independently matched to the actual final race receipt.
Full internal/cmd Go vet passes. Automatic authenticated peer redirects are
refused; invalid webhook HTTP statuses are rejected before database narrowing.
Ordinary peer forwarding, proxy 307/308 response passthrough and webhook retries
remain covered. This does not qualify the complete first-party security policy.

The integrated checks exposed two additional composition problems. The catalog
page exceeded its existing complexity ceiling; extracting its cluster banner
restores the ceiling without changing rendering. The controller window's inline
cache key failed lint; the shared factory now preserves the same user, superuser
and role components. Canonical route generation adds the existing scanner
database-status route and refreshes the descriptive configuration route entry.
Full frontend qualification now passes all 350 files and 2,444 tests, TypeScript,
lint, production build, CSP and unchanged bundle budgets. The seven selected
server route/authentication/CSRF parents pass with 244 named results.
The API comparison marker now records 855 operations and 829/829 mounted routes,
derived from the actual contract checks. No API operation was added or removed
by this inventory refresh. Original failures remain retained separately.

Scoped database progress has also advanced: both owned PostgreSQL 17 and 18
databases pass canonical 079 and the 100,000-chart 080 measurement. PostgreSQL 18
receipt `8ae47dd86eea1d0e4f2f992859f02b45be2fe4e9116ee1108312baa5e77dbcfa`
records 39 held gates and 34 default guards, 1.599 seconds for the migration and
exact cleanup leaving one witness fixture. WAL measurements are shared-cluster
observations. The PostgreSQL 17 interruption attempt instead fails on a
`schema_migrations` TRUNCATE lock before creating an index; it provides no
INVALID-index recovery credit. Reviewed admission corrections for both majors
are queued but have not run. The earlier valid-index diagnostic and normal
canonical down/reset are distinct from interrupted-index recovery.

The schema/JSON strict check on source `45367957` passes with 189 request entries,
206 columns and 443 modules. This remains scoped to that source; later source
changes need a fresh current inventory before claiming whole-current coverage.
Exact closed-cache cleanup retained the original partial failure, then removed
the independently authenticated remainder. It recovered approximately 11.2 GiB
of allocated compiler archives without deleting source, modules, evidence,
images, databases or PVCs. Later compiler builds may recreate cache paths.
The default 20 GiB **and** 20 percent capacity thresholds remain unchanged.

Open integration work includes all seven first-party candidate images and their
signatures, genuine scanner/Flux/agent
runtime, current collector ingestion, schema-81 interruption/recovery and wider
database cases, complete CLI/CNPG upgrade and restore, current browser/support
matrix, fresh whole-source security enforcement and a coherent candidate install.
Historical v1.2.1 waivers and qualification deferrals retain their original scope;
this checkpoint does not extend them to a future release.
