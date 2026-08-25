# Accessibility release-candidate checklist

This is the required human qualification protocol for Astronomer's supported
operator workflows. Automated lint, axe, unit, and browser tests remain release
gates, but they do not replace assistive-technology testing. A release candidate
cannot claim WCAG 2.2 AA qualification until the results below are attached to
the release evidence.

## Test record

Record this header once per candidate:

- release version and immutable commit SHA;
- frontend image digest and browser compatibility-matrix revision;
- tester, date, locale, viewport, zoom, color scheme, and operating system;
- browser and exact version;
- screen reader and exact version;
- test data/tenant and adopted-cluster fixture identifiers;
- issue links for every exception, including owner, severity, workaround, and
  expiry release;
- screenshots or video only after checking that they contain no credentials,
  tokens, Secret values, kubeconfigs, or customer data.

Minimum assistive-technology matrix:

| Platform | Browser | Assistive technology | Required |
| --- | --- | --- | --- |
| Windows | Chrome | NVDA, current supported version | Yes |
| Windows | Edge | Narrator, current supported version | Yes |
| macOS | Safari | VoiceOver from the supported macOS release | Yes |
| iOS | Safari | VoiceOver at the mobile compatibility viewport | Critical workflows |

Run desktop workflows at 100% and 200% zoom. Run responsive checks at 320 CSS
pixels without horizontal page scrolling, except inside intentionally
two-dimensional data grids, terminals, diffs, or code editors.

## Global keyboard and focus checks

For every workflow below, complete these checks without a pointer:

- Start at the browser address bar, enter the application, and reach the main
  content through a visible skip link.
- Tab order follows visual and task order; no hidden, disabled, or inert control
  receives focus.
- Every focused control has a visible indicator with sufficient contrast; the
  indicator is not obscured by sticky headers, drawers, toasts, or banners.
- Buttons, links, menu items, tabs, switches, checkboxes, rows, tree nodes, and
  disclosure controls work with their native expected keys.
- Escape closes only the topmost dismissible layer and returns focus to its
  opener. Destructive confirmation cannot be bypassed with an unrelated Enter.
- Dialogs and drawers trap focus while open, have a programmatic name and
  description, and restore focus on close.
- No keyboard trap occurs in YAML editors, terminals, log viewers, tables,
  comboboxes, or date/time controls. Documented editor escape chords work.
- Route changes and asynchronous panels move or announce focus deliberately;
  focus never falls back to the document body without an announced context.
- Loading, success, warning, retry, validation, permission-denied, offline, and
  terminal-failure messages are announced once at the correct priority.
- At 200% zoom and 320 CSS pixels, controls remain reachable, labels retain
  essential meaning, and sticky actions do not cover content.

## Screen-reader checks

Run the critical workflows with speech enabled and the screen hidden where
practical:

- Page title, one level-one heading, landmarks, breadcrumbs, and current
  navigation location identify the page unambiguously.
- Controls have concise accessible names; icon-only actions announce the action
  and target, not an icon filename or a generic “button”.
- Required state, invalid state, descriptions, constraints, and inline errors
  are associated with their fields. Error summaries link to invalid fields.
- Tables announce context, column headers, sort state, selected row, pagination,
  and row actions. Virtualized content does not produce false row counts.
- Status is not conveyed by color alone. Badges, charts, gauges, topology, and
  diff additions/removals have equivalent text.
- Secrets remain masked and reveal/copy controls announce current state. Secret
  values never appear in an accessible name or live region.
- Time, resource quantities, cluster/namespace scope, and operation status are
  understandable without relying on layout.
- Toasts do not steal focus; persistent failures remain discoverable after a
  toast expires.

## Critical workflow scripts

For each script, record pass/fail for keyboard, NVDA, VoiceOver, responsive
layout, and error recovery. A critical or serious issue blocks release.

### Bootstrap, login, and session recovery

1. Identify platform branding, status banner, username, password, SSO choices,
   and recovery link.
2. Submit empty and invalid fields; locate and correct every error.
3. Complete local login, forced TOTP enrollment, TOTP challenge, recovery-code
   fallback, logout, expired-session refresh, and password reset.
4. Verify password-manager semantics, masked secrets, and no announcement of
   credential values.

### Adopted-cluster registration and reconnect

1. Complete every wizard step and review privilege profile and install impact.
2. Copy the install command, move backward/forward without losing state, and
   cancel safely.
3. Observe waiting, connected, incompatible, token-expired, offline, retrying,
   and reconnected states.
4. Reach troubleshooting guidance and retry without starting a duplicate
   registration.

### Resource explorer and workload operations

1. Select cluster, resource kind, namespace, filters, sort, and pagination.
2. Open a resource, traverse overview/YAML/conditions/events/related/logs/exec,
   and follow an owner reference back.
3. Create and edit with guided fields and YAML; provoke validation, forbidden,
   conflict, dry-run, and offline errors; inspect the diff before apply.
4. Scale and restart a workload, stream logs, open/close exec, and verify focus
   recovery.
5. Delete a namespaced resource and a namespace; read impact, enter the exact
   typed confirmation, cancel once, then confirm.

### RBAC binding and denial

1. Create a global/project/cluster/namespace binding with searchable user, role,
   and scope selectors.
2. Review effective permissions before saving and remove the binding with typed
   confirmation.
3. As the restricted user, encounter permission denial and disabled actions
   whose reason is announced and links to remediation.

### Flux delivery lifecycle

1. Create and verify a source without exposing credential values.
2. Create a bundle/version/target, review placement and incompatible clusters,
   then begin a rollout.
3. Approve, pause, resume, retry, abort, and roll back; status updates must not
   reset focus.
4. Navigate from rollout to cluster deployment and Flux inventory evidence.

### Backup and restore

1. Configure/test backup storage, create a backup, and inspect progress/history.
2. Start restore, understand overwrite/downtime impact, complete typed
   confirmation, and distinguish management-plane from member-cluster restore.
3. Recover from validation, unavailable-storage, and terminal-operation errors
   without re-entering unrelated fields.

### Security scan and findings

1. Start a CIS scan, observe pending/running/disconnected/resumed/timed-out
   states, and cancel a run.
2. Filter findings, expand evidence/remediation, and export without losing table
   position.
3. Verify severity, pass/fail, and trend information has non-color text.

### Unified logging

1. Create/test an output, inspect capability labels, and distinguish native
   query, secure link-out, and shipping-only modes.
2. Query logs, change time/namespaces/direction/limit, open context, save/load a
   search, share the URL, and start/stop live tail.
3. Verify new log rows do not steal focus or flood announcements; provider and
   partial-result failures remain discoverable.

### Direct/proxy access and decommission

1. Generate allowed access material, understand expiry/scope, copy it, and
   verify secret-safe announcements.
2. Decommission a cluster after reviewing affected Flux assignments, backups,
   active sessions, and queued operations.
3. Complete typed confirmation; observe progress, retryable/terminal failures,
   and the final tombstone state.

## Visual and cognitive checks

- Text and non-text contrast meet AA in light/dark and forced-colors modes.
- Content works at 200% text zoom and 400% page zoom/reflow where applicable.
- Motion honors `prefers-reduced-motion`; progress does not rely on animation.
- Timeout/session warnings provide notice and a keyboard-operable extension
  when policy permits.
- Instructions identify controls by name, not position, shape, or color alone.
- Destructive language names the object, scope, downstream impact, and recovery
  path in plain language.

## Exit criteria and evidence

- No open critical/serious accessibility defect in a supported critical
  workflow.
- Moderate defects have an approved owner, workaround, expiry release, and
  documented impact; repeated deferral blocks qualification.
- The complete matrix and workflow results are attached to immutable release
  evidence with issue links and sanitized artifacts.
- Automated lint, axe, keyboard unit tests, responsive visual tests, and critical
  live journeys pass on the same commit and image digest.
- The release approver signs only after independently checking the evidence.
