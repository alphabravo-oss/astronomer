# Migration dependency pins (P0.4 preflight)

Canonical source of versions for P1.1 (package.json + lockfile) and P4.8
(`@tanstack/react-pacer`). P1.1's committed
package.json must match this list exactly. Do not bump versions here without
re-running the audit preflight below.

Initially resolved on 2026-07-15; application dependency pins below were
requalified on 2026-09-11 against registry.npmjs.org using Node 22.22.2.

## Pinned package list

One line per package: name | exact resolved version | policy (range written into package.json) | audit result.

| Package                             | Resolved version | Policy                                       | Audit |
| ----------------------------------- | ---------------- | -------------------------------------------- | ----- |
| vite                                | 8.3.0            | caret (`^8.3.0`)                             | clean |
| @vitejs/plugin-react                | 6.1.1            | caret (`^6.1.1`)                             | clean |
| @tanstack/react-router              | 1.170.18         | caret (`^1.170.18`)                          | clean |
| @tanstack/router-plugin             | 1.168.20         | caret (`^1.168.20`)                          | clean |
| @tanstack/react-form                | 1.33.2           | caret (`^1.33.2`)                            | clean |
| @tanstack/react-pacer               | 0.23.0           | **exact** (`0.23.0`, no caret — pre-1.0, D6) | clean |
| @tanstack/react-table               | 9.2.4            | caret (`^9.2.4`)                              | clean |
| @wterm/core                         | 0.5.0            | **exact** (`0.5.0`, pre-1.0)                    | clean |
| @wterm/dom                          | 0.5.0            | **exact** (`0.5.0`, pre-1.0)                    | clean |
| @wterm/react                        | 0.5.0            | **exact** (`0.5.0`, pre-1.0)                    | clean |
| tailwindcss                         | 4.3.3            | caret (`^4.3.3`)                             | clean |
| @tailwindcss/vite                   | 4.3.3            | caret (`^4.3.3`)                             | clean |
| @tailwindcss/forms                  | 0.5.11           | caret (`^0.5.11`)                            | clean |
| tailwind-merge                      | 3.6.0            | caret (`^3.6.0`)                             | clean |
| @fontsource-variable/inter          | 5.2.8            | caret (`^5.2.8`)                             | clean |
| @fontsource-variable/jetbrains-mono | 5.2.8            | caret (`^5.2.8`)                             | clean |
| vitest                              | 5.0.0            | caret (`^5.0.0`)                             | clean |
| jsdom                               | 30.0.1           | caret (`^30.0.1`)                            | clean |
| eslint                              | 9.39.5           | caret (`^9.39.5`)                            | clean |
| typescript                          | 6.0.3            | caret (`^6.0.3`)                             | clean |

TypeScript intentionally remains at 6.0.3: the current `typescript-eslint`
8.70.0 peer contract is `>=4.8.4 <6.1.0`, while TypeScript 7.0.2 is outside
that contract. Do not force or suppress that peer mismatch. Upgrade to 7 only
when the lint toolchain publishes a compatible release.

ESLint intentionally remains at 9.39.5: the current `eslint-plugin-jsx-a11y`
6.10.2 peer contract ends at ESLint 9, and no compatible prerelease exists.
Keep the accessibility rules enabled; upgrade to ESLint 10 only with a supported
plugin release, never by forcing the peer graph or dropping lint enforcement.

## Audit preflight result

2026-09-11: `npm install --strict-peer-deps` on Node 22.22.2 / npm 10.9.7
resolved the current application graph without peer warnings, audited 755
packages, and reported zero vulnerabilities. `npm outdated` lists only the
peer-blocked TypeScript and ESLint/@eslint/js major upgrades described above.
Table 9 uses native feature composition and `useTable`, without the former
snapshot controller. The aligned wterm runtime is loaded only when a console
tab opens; console tab switches preserve mounted sessions.

Tailwind 4 uses its native CSS-first theme and the dedicated Vite plugin. The
JavaScript configuration and PostCSS bridge were removed rather than retained
as compatibility layers; v3 utility aliases were migrated to their v4 names.

Historical migration preflight (2026-07-15):

`npm install` of the full candidate set above (with `react@19.2.7` /
`react-dom@19.2.7` as peers, matching the repo's `^19.0.0` range) in a scratch
directory resolved 199 packages; `npm audit --audit-level=moderate` reported
**found 0 vulnerabilities** (exit 0). No advisory exists on any pre-1.0
TanStack package, so nothing blocks the P4.8 adopting phase per D6.

Vite 8 resolves TypeScript path aliases through native `resolve.tsconfigPaths`;
the former `vite-tsconfig-paths` plugin was removed. The only install deprecation
remaining in this graph is `whatwg-encoding@3.1.1`, transitively used by jsdom.

## Base image digests (D16)

- `node:22-alpine` current multi-arch index digest (for the P6.1 Dockerfile pin):
  `node:22-alpine@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2`
  Trivy (HIGH/CRITICAL): 0 CRITICAL, 2 HIGH — CVE-2026-33671 (picomatch) and
  CVE-2026-48815 (sigstore), both in the bundled npm CLI's node_modules (build
  stage only; nothing from this image ships in the final nginx stage).
- `nginx:1.27-alpine@sha256:65645c7bb6a0661892a8b03b89d0743208a18dd2f3f17a54ef4b76fb8e2f2a10`
  (the repo's current pin in `deploy/nginx/Dockerfile.nginx`) **no longer scans
  clean**: Trivy reports **2 CRITICAL + 35 HIGH** (libxml2, musl, openssl,
  nghttp2, zlib, curl, …). Per D16 the fallback fires: P6.1 must bump **both**
  `frontend/Dockerfile` stage 2 and `deploy/nginx/Dockerfile.nginx` to the
  current `nginx:1.29-alpine` digest in one commit.
- `nginx:1.29-alpine` current multi-arch index digest (pre-resolved for P6.1):
  `nginx:1.29-alpine@sha256:5616878291a2eed594aee8db4dade5878cf7edcb475e59193904b198d9b830de`
  Trivy (HIGH/CRITICAL): 0 CRITICAL, 13 HIGH (libexpat x4, curl/libcurl x4,
  libcrypto3/libssl3 x2, nghttp2-libs, c-ares, libxml2 — all awaiting upstream
  Alpine package bumps as of 2026-07-15; no fixed nginx-alpine tag exists yet).
  **Exception recorded**: 1.29 clears both CRITICALs but is not fully HIGH-clean
  today; P6.1 must re-resolve the newest `nginx:1.29-alpine` digest and re-scan
  at execution time, and if HIGH findings persist in the newest digest, handle
  them per the repo's existing Trivy CI policy before first CI push.

All scans run 2026-07-15 with `aquasec/trivy:latest` at `--severity HIGH,CRITICAL`.

### P6.1 execution-time re-scan (2026-07-15, later the same day)

Re-resolved `nginx:1.29-alpine` — digest unchanged (`sha256:5616878291a2eed…`).
Alpine had since shipped fixes for several of the 13 HIGHs (libexpat, libssl3,
libxml2, nghttp2-libs), so `--ignore-unfixed` no longer excludes them. Per the
repo's Trivy CI policy (fixable HIGH/CRITICAL fail the build), the frontend
Dockerfile's nginx stage runs `apk upgrade --no-cache`; the built image scans
**0 findings** at `--severity HIGH,CRITICAL --ignore-unfixed`.
