# Latest stable build tooling

Status: accepted, 2026-10-09. Supersedes the runtime selection in
[frontend application stack](frontend-application-stack.md).

Use the latest final Go release and Node Current release for application builds.
The frontend ships static assets; Node is a build dependency. Node Current is
selected deliberately and must pass the same type, lint, test, CSP and bundle
gates as the previous baseline. npm is selected independently from Node's
bundled npm and installed at its exact selected version.

`deploy/toolchain/versions.json` owns selected versions, upstream discoveries,
immutable artifact identities, consumers and temporary compatibility exceptions.
`make versions-check` validates that authority offline. `make updates` only
discovers updates and writes review evidence; it never changes source consumers.
Daily discovery and weekly platform/frontend triage include major releases.
Release preflight rejects stale discovery and expired exceptions. Released
manifests remain separate immutable signed records of actual released bytes.

Latest stable does not authorize unattended database, cluster or controller
migrations. A component that cannot adopt the discovered target has a named
owner, specific evidence, review deadline and exit condition. This applies to
TypeScript 7 (typescript-eslint's current peer constraint), ESLint 10
(jsx-a11y's current peer constraint), Helm SDK 4 and stateful platform majors.
Discovery continues for deferred components so an upstream compatibility fix
can remove the exception.

The application stack, generated API boundary, TanStack ownership,
accessibility restrictions and strict gates remain the accepted architecture.

Review on every compiler/Node major and by 2026-11-09.
