---
status: "accepted"
date: 2026-10-20
deciders: ["AlphaBravoCompany"]
title: "Tailwind v4 CSS-first configuration (no tailwind.config.ts)"
tags: [frontend]
---

# Tailwind v4 CSS-first configuration (no tailwind.config.ts)

## Context

The guide (`10-typescript.md`) requires Tailwind v4 with the Oxide engine and says configuration lives at `tailwind.config.ts`. Tailwind v4 moved configuration into CSS (`@import "tailwindcss"; @theme { ... }`) and the `@tailwindcss/vite` plugin; a JS config file is a v3 holdover that v4 only honours through a legacy `@config` directive. Astronomer's design tokens live in `frontend/src/styles/globals.css` under `@theme inline`, and `cn()` is `clsx` + `extendTailwindMerge`.

## Decision Drivers

- Follow the Tailwind v4 documented configuration model.
- Keep one source of truth for tokens (CSS variables also drive Radix/shadcn primitives).

## Considered Options

- CSS-first configuration in `globals.css` (this ADR).
- Add a `tailwind.config.ts` loaded through `@config`.

## Decision Outcome

Chosen option: "CSS-first", because it is Tailwind v4's native model; the guide's `tailwind.config.ts` wording predates v4 and is satisfied in spirit by `globals.css` being the single configuration file.

### Positive Consequences

- No duplicated token definitions; Oxide/Vite plugin path stays default.

### Negative Consequences

- Engineers coming from v3 look for a JS config that does not exist; this ADR is the pointer.

## Consequences

Theme tokens, plugins (`@plugin "@tailwindcss/forms"`) and content detection are declared in `frontend/src/styles/globals.css`.

## Pros and Cons of the Options

### CSS-first

- Good, because it is the v4 default and keeps tokens next to the CSS variables.
- Bad, because the guide text names a different file.

### tailwind.config.ts via @config

- Good, because it matches the guide text.
- Bad, because it is the legacy compatibility path in v4.

## Links

- Guide `10-typescript.md`; `frontend/src/styles/globals.css`.
