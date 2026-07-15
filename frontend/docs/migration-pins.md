# Migration dependency pins (P0.4 preflight)

Canonical source of versions for P1.1 (package.json + lockfile), P4.7 (`@tanstack/db`
/ `@tanstack/react-db`), and P4.8 (`@tanstack/react-pacer`). P1.1's committed
package.json must match this list exactly. Do not bump versions here without
re-running the audit preflight below.

Resolved on 2026-07-15 against registry.npmjs.org (npm 9.2.0, node v22.22.1).

## Pinned package list

One line per package: name | exact resolved version | policy (range written into package.json) | audit result.

| Package | Resolved version | Policy | Audit |
|---|---|---|---|
| vite | 7.3.6 | caret (`^7.3.6`) | clean |
| @vitejs/plugin-react | 5.2.0 | caret (`^5.2.0`) | clean |
| @tanstack/react-router | 1.170.18 | caret (`^1.170.18`) | clean |
| @tanstack/router-plugin | 1.168.20 | caret (`^1.168.20`) | clean |
| @tanstack/react-form | 1.33.2 | caret (`^1.33.2`) | clean |
| @tanstack/store | 0.7.7 | caret (`^0.7.7`) | clean |
| @tanstack/react-store | 0.7.7 | caret (`^0.7.7`) | clean |
| @tanstack/db | 0.6.14 | **exact** (`0.6.14`, no caret — pre-1.0, D6) | clean |
| @tanstack/react-db | 0.1.92 | **exact** (`0.1.92`, no caret — pre-1.0, D6) | clean |
| @tanstack/react-pacer | 0.22.1 | **exact** (`0.22.1`, no caret — pre-1.0, D6) | clean |
| @fontsource-variable/inter | 5.2.8 | caret (`^5.2.8`) | clean |
| @fontsource-variable/jetbrains-mono | 5.2.8 | caret (`^5.2.8`) | clean |
| vitest | 4.1.10 | caret (`^4.1.10`) | clean |
| jsdom | 25.0.1 | caret (`^25.0.1`) | clean |
| vite-tsconfig-paths | 5.1.4 | caret (`^5.1.4`) | clean |

## Audit preflight result

`npm install` of the full candidate set above (with `react@19.2.7` /
`react-dom@19.2.7` as peers, matching the repo's `^19.0.0` range) in a scratch
directory resolved 199 packages; `npm audit --audit-level=moderate` reported
**found 0 vulnerabilities** (exit 0). No advisory exists on any pre-1.0
TanStack package, so nothing blocks the P4.7/P4.8 adopting phases per D6.

Non-blocking install warnings (deprecations, not advisories): `whatwg-encoding@3.1.1`
(transitive via jsdom) and `tsconfck@3.1.6` (transitive via vite-tsconfig-paths).

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
