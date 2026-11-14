# Fresh-clone portability verification — 2026-11-14

This receipt supports [resuming development](../../../docs/resuming-development.md)
and the single [working plan](../../CURRENT.md). It is not a separate plan.
[verification.json](verification.json) records exact source identities, checks,
fixes and limits; [startup.json](startup.json) records the isolated runtime result.

## Evidence

A separate clone was obtained from public GitHub at
`dded685dc385be61f603b371c483499f037113b1`. Go used new, empty module and build
caches, `GOWORK=off`, the public Go proxy/checksum database, no private-module
exceptions and no global Git credential configuration. Module downloads and
`make build` succeeded for server, worker, agent and CLI. The published Identity
v0.1.0 dependency resolved without the existing local Identity checkout.

`npm ci` installed 686 packages from the committed lockfile using an empty npm
cache and empty user/global configuration. The production frontend build,
type check, CSP check, bundle budgets and generated OpenAPI checks passed.
No local Go replacement or external/dangling repository symlink was found.

Two newly created containers provided PostgreSQL 17.11 and Redis 8.10.2 with
ephemeral data. The compiled migrator initialized schema 88. The compiled server
and worker reached readiness/metrics; a new local bootstrap administrator could
log in and read its authenticated user profile. No provider was configured.

Before the web server started, `frontend/public/monaco` was absent. Invoking
`npm run dev` generated and served the editor manifest, editor/worker JavaScript
and terminal WASM. The old dev launcher invoked Vite directly, bypassing that
`predev` step; `scripts/dev.sh` now uses the verified npm lifecycle. Bash syntax
and ShellCheck pass; the readiness loop still permits exactly 300 attempts.
All test processes and both owned containers were removed afterward.

## Previously local-only source

The standalone full-test infrastructure had no Git remote. Its entire original
15-commit history and 38 source files are now preserved in the existing private
`alphabravo-oss/astronomer-testbed` repository, under `full-test-iac/` on `master`.
The original tip is an ancestor of the published canonical branch. The existing
root harness was retained and marked historical. Only checkout-relative defaults
and documentation were adjusted for the new directory. Shell/Python checks,
Terraform formatting and both provider validations, and Ansible syntax passed.
No cloud resources were created or changed.

The private testbed full-history secret scan passes. Its one historical false
positive was an empty DigitalOcean token followed by the public region setting.
The exception matches that exact two-line template; a nonempty token fixture is
still detected. No directory or credential class was excluded.

The existing Charlie salvage commit was one local-only commit, 682 commits
behind its product's current main. It was preserved as the private remote branch
`archive/local-wip-20261020`, after scanning the newly published commit. It was
not merged into the newer product and is not qualified implementation evidence.

## Limits

This was a fresh checkout and empty dependency caches on the existing Linux
host, not a newly installed OS. Runtime checks used HTTP and disposable database/
queue services; there was no browser UI suite, full telemetry-stack deployment,
cloud campaign, signed release or live Kubernetes cutover. Public development
keys were used only for the isolated fixture and triggered their expected
insecure-development-key diagnostic. Credentials and raw runtime logs are not
included in this receipt.

Source portability does not certify live-state recovery. A database/PVC backup,
the corresponding encryption/recovery keys, deployment configuration and any
Terraform state must remain available in protected off-node storage. Their
current off-node availability and restore proof remain open under CURRENT S2.
