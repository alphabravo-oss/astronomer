---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "TanStack Router v1 as the dashboard router"
tags: [frontend, routing]
---

# TanStack Router v1 as the dashboard router

## Context

The Technology Selection Guide (`10-typescript.md`, ADR-0006) names react-router v7 Data Mode as the canonical router and TanStack Router v1 as the labelled alternative that MUST be recorded in an ADR. Astronomer's dashboard has 357 file-based route modules under `frontend/src/routes`, a generated `routeTree.gen.ts`, `beforeLoad` auth guards with return-to handling, typed search params and automatic code splitting through `@tanstack/router-plugin`. Constellation, the sibling product, uses the same router, so the two consoles share conventions, components and reviewer expertise.

## Decision Drivers

- Zero functional gain from rewriting 357 route files; the migration is XL effort with user-visible regression risk.
- Product-family consistency with Constellation (shared primitives, shared engineers).
- Type-safe search params and loaders that already cover the guide's "central auth redirect" and "pending UI" requirements.
- The guide explicitly sanctions the alternative when recorded.

## Considered Options

- Keep TanStack Router v1 (file-based) and record this ADR.
- Rewrite to react-router v8 Data Mode (`createBrowserRouter`, `routes.tsx`).

## Decision Outcome

Chosen option: "Keep TanStack Router v1", because it satisfies every functional requirement the guide attaches to routing, keeps the product family on one router, and avoids an XL rewrite with no user value. The repository MUST NOT add react-router; mixing routers is prohibited.

### Positive Consequences

- No migration cost; existing guards, search-param typing and code splitting stay.
- Shared conventions with Constellation.

### Negative Consequences

- Guide examples written for react-router Data Mode need translation (`beforeLoad` instead of loaders, `useRouterState` instead of `useNavigation`).

## Consequences

Routing stays file-based under `src/routes`. Auth redirects remain centralised in the dashboard layout `beforeLoad`. Review cadence: revisit only if react-router ships a capability the product needs and TanStack lacks.

## Pros and Cons of the Options

### Keep TanStack Router v1

- Good, because it is already proven across 357 routes and 55 Playwright specs.
- Good, because Constellation uses it.
- Bad, because it is the guide's alternative, not its default.

### Rewrite to react-router v8

- Good, because it would match the guide's canonical choice.
- Bad, because the rewrite is XL with zero user-facing benefit.
- Bad, because it would split the product family across two routers.

## Links

- Guide `10-typescript.md` (routing), guide ADR-0006.
- `docs/architecture/decisions/frontend-application-stack.md` (prior stack decision).
