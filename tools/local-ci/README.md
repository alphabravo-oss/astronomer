# Pinned Local CI tooling

`make local-ci-install` installs the lockfile-pinned runner and applies the
version-bound compatibility patches. `npm ci --prefix tools/local-ci`
also applies it through `postinstall`. Run `npm test --prefix tools/local-ci`
to verify the patch contracts, executable modes and dependency results.

Use the same Node runtime for installation and execution. After switching Node
versions, run `npm ci --prefix tools/local-ci` again. Native dependencies built
under Node 22 can crash at teardown when loaded by Node 24, even when the runner
finishes its checks. `make local-ci-install` runs an isolated dependency-import
preflight before starting any workflow, catching this mismatch early; `--help`
does not load those dependencies and is not a sufficient runtime check.
On distro-managed hosts, the native build tool can additionally select system
headers and link the system `libnode` despite a different `node` on `PATH`.
Use a consistently paired runtime/build toolchain; this runner supports Node
22 or later. The controller's Node version does not override the workflow's
explicit Node 24 setup inside its job containers. Do not suppress the preflight
or reinterpret a signal termination as a successful workflow command.

## Workspace mode workaround (0.18.1)

The runner copies the candidate, initializes its temporary Git index, then
applies `chmod -R 777` to the workspace. This makes ordinary source files
executable. Generators that recreate these files restore their normal modes,
correctly failing Astronomer's source-tree stability guard despite unchanged
contents. The September 22 Plan 026 backend run reproduced this failure.

`apply-runner-patch.mjs` replaces that one command with `chmod -R a+rwX`.
Directories remain traversable/writable; files remain readable/writable; only
files that were already executable retain execution permission. This applies
only to the runner's private workspace and diagnostics, never the host source.
The source hash still detects real content and executable-mode changes.

The patch is idempotent and fails closed on another package version or an
unexpected/ambiguous command. On a runner upgrade, review its source and remove
this workaround if upstream preserves modes. Do not weaken the evidence guard
or blindly carry this replacement forward.

## Dependency result context (0.18.1)

The runner expands `toJSON(needs)` to an empty JSON string and collects only job
outputs, not results. This cannot execute the unchanged PR aggregate. The
version-bound `patch-runner-needs.mjs` records actual job results and exposes
the canonical `{result, outputs}` dependency context. A failed matrix entry
stays failed even if a later entry succeeds. Missing result evidence fails
closed; results are never fabricated as successful.

Tests cover exact/idempotent patching, version/source mismatch rejection,
both matrix completion orders, output preservation, failure/skip/cancellation,
and execution of the real PR aggregate for passing and failing dependencies.
All patch anchors are validated before any installed dependency is rewritten.
Review and remove these repairs when upgrading the pinned runner. The runner
still aborts later waves after a failed mixed first wave; a skipped aggregate
is not passing qualification, and local evidence is not protected GitHub signoff.
