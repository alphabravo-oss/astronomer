---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "A1 Operator Console UI and the shared AlphaBravo UI kit"
tags: [frontend, design-system]
---

# A1 Operator Console UI and the shared AlphaBravo UI kit

## Context

The 2026-10-20 UI audit (`readiness-assessment-2026-10-20/ui/ui-inventory.md`, 128 screenshots across Astronomer and Constellation) found both consoles built on the same stack and the same shell, with everything above the tokens drifted: status colors, fonts, density, button kits, stat cards, empty/loading states, naming and the amount of chrome per page. Astronomer specifically read as a developer tool: ~75 navigation items across two sidebars with no hierarchy cue, three names for installed tooling, an operator-branding blue hijacking a neutral system, a floating assistant pill over content, an uppercase command palette, KPI tiles contradicting sidebar counts, raw pod/node identifiers, settings three layers deep, a crowded header and flat vertical rhythm (critique items 1–10 in §4.1).

The owner chose option **A1 "Operator Console"** for Astronomer and **C1 "Triage Desk"** for Constellation (`UI-OPTIONS.md` §7), with a shared foundation (§1): Inter Variable + JetBrains Mono Variable self-hosted, one three-mode server-synced theme, Charlie as a header icon + drawer, operator branding limited to logo + one accent.

## Decision Drivers

- The two products must feel related in most elements and distinct in a few identity carriers, without drifting again.
- Enterprise-grade refinement with minimal change to how users navigate ("keep most of what it is").
- One place to fix shell, tokens, tables and states for both products.
- Guide chapter 10 constraints: React 19 + Vite + Tailwind v4 + Radix/shadcn pattern + TanStack; styled primitives copied into the repo, not a styled npm package.

## Considered Options

- **A1 Operator Console** with a copied `src/ui/` kit (this ADR).
- A1 with the kit published as `@alphabravo/ui` now.
- A2–A5 (Control Plane, Workspace, Calm Enterprise, Command Center).

## Decision Outcome

Chosen option: "A1 with a copied `src/ui/` kit", because A1 fixes the hierarchy, naming and chrome problems without changing the navigation model users know, and a copied directory ships this week while the Connect-Web client generation that a package would ride on is still Phase 2.

Concretely:

1. **Shared kit** at `frontend/src/ui/` (documented in `docs/ui/KIT.md`): tokens (neutral, brand slots, status, severity, elevation, motion, z-index, density, shell geometry, type scale with an 11 px floor), `kit.css` component layer, and the components `AppShell`, `SidebarFrame`/`Rail`, `Header`/`Breadcrumbs`/`SearchTrigger`, `CommandPalette`, `PageHeader`, `StatCard`, table chrome (`TableToolbar`, `ExportMenu`, `FilterChip`, `Pagination`, `RowActions`, `EntityLink`), the form kit (Radix `Select`/`Checkbox`/`Switch`, cmdk `Combobox`, `FormShell`), `ConfirmDialog` (typed confirmation), states (`Empty`/`Loading`/`Error`/`NotFound`/`Forbidden`), `Tabs`, `VerdictBanner`, `Collapse`, `KitToaster`, `ThemeProvider`. Product identity is injected through `ProductConfig`. The kit imports nothing from product code and is copied verbatim into Constellation.
2. **Navigation**: a global rail of seven pillars (Overview, Clusters, Delivery, Observability, Security, Access, Settings) that stays visible, and a cluster panel of six groups (Overview, Workloads, Networking, Storage, Policy & access, Manage) beside it in cluster context. Counts leave the sidebar for the cluster overview's Inventory section; "All clusters" is a header breadcrumb; breadcrumbs render only at depth ≥ 2; the four header icon buttons become one "Cluster actions" menu; scope pickers appear only in cluster context.
3. **Naming**: sentence case everywhere; Cluster Tools / Platform Tools / Tool UIs → **Tools** (installed) and **Catalog** (installable); Estate → **Fleet**; Flux deployment records → **Releases** (the existing progressive-rollout noun **Rollouts** stays, so Kubernetes Deployments are unambiguous — a deviation from the option text, which assumed only one of the two existed); RBAC → **Access control**; Audit Log → **Audit log**.
4. **Color**: operator `branding.primary_color` is a theme-level accent applied through `applyBrandAccent` to the mark, the active nav indicator and primary buttons only; the backend's seeded placeholder (`#0066CC`) counts as unconfigured. Default accent violet with the blue → violet gradient on the mark. Links, selected rows, focus and pagination are neutral.
5. **Charlie**: header icon + `⌘J` + right drawer; the floating pill is removed. The cluster switcher loses its `⌘J` binding.
6. **Dashboard**: fleet verdict strip, clusters-by-health strip, capacity bars, agent status, delivery fleet strip with attention items; every number reads from the source its list is paged from.
7. **Settings**: two layers (settings sidebar → page); the hub redirects to General; the SSO signpost is a banner only while no provider is configured.
8. **Tables**: 44 px rows, 32 px selection column revealed on hover/selection, Name never truncates, raw ids as `EntityLink`/`EntityName` with the full value on hover, one `Export ▾`, audit actions humanised with the raw key as secondary text.
9. **Theme**: the kit `ThemeProvider` with Astronomer's server-synced storage adapter (`PUT /auth/me/preferences`).

### Positive Consequences

- Both products render from one token and component contract; the next drift is a copy diff, not a redesign.
- The ten audit critiques are addressed structurally (nav model, naming registry, brand slots, kit states) rather than page by page.
- Lint already confines Radix to `src/components/ui` and `src/ui`, so product code keeps composing primitives.

### Negative Consequences

- A copied kit must be synchronised by hand until Phase 2 extracts a package; both repos carry `docs/ui/KIT.md` to say so.
- Legacy `src/components/ui` primitives (ActionButton, DataTable, EmptyState) remain and adopt kit styles/tokens; they are not replaced wholesale this round.
- Unit tests that encoded the old labels and sidebar internals were rewritten; the Playwright live tier is the proof of the new model.

## Pros and Cons of the Options

### A1 + copied kit

- Good, because it keeps every structural element users know (tables, wizard, terminal) and fixes hierarchy, naming and chrome.
- Good, because a copied directory needs no publishing pipeline today.
- Bad, because two copies can drift between syncs.

### A1 + published package now

- Good, because one source of truth with versioned consumption.
- Bad, because it front-loads package tooling before the Phase 2 client generation it should ship with.

### A2–A5

- Good, because each has a signature screen (topology, scope control, calm register, keyboard model).
- Bad, because each is L effort and changes how users navigate, contrary to the brief.

## Links

- `readiness-assessment-2026-10-20/UI-OPTIONS.md` §1, §2 A1, §5, §7; `ui/ui-inventory.md` §1, §4.1.
- `docs/ui/KIT.md`; ADR 0006 (Tailwind v4 CSS-first), ADR 0001 (TanStack Router).
