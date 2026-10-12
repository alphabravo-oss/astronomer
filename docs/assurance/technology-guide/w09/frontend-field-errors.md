# W09 field validation receipt

The inhibition dialog maps only owned `validation_error` field names to local
control names and local messages. It ignores upstream messages, values, unknown
fields and prototype keys; reads at most 32 entries; and deduplicates controls.
The shared error summary retains server field links across edits, focuses the
summary after failure, and focuses the named control on activation. A successful
retry clears the summary. Empty matcher values remain meaningful v1 strings.

Before the change, the new real panel regression failed because the field link
was absent and the upstream private fixture message appeared. The fixed targeted
Vitest command ran eight tests across the API validation adapter, shared summary
and production inhibition panel. The API fixture traverses the real Axios error
interceptor; this unit adapter is not a network socket test.

`npm run test:browser-components` with `PLAYWRIGHT_COMPONENT_PORT=3135` ran all
21 cases, three viewport projects, workers=1, retries=0. The new browser case
loads the production dialog/hooks/generated client and intercepts HTTP responses
at the browser boundary. It verifies safe persistent field text, summary/control
focus, two Enter key presses during a pending request producing one submission,
and a successful retry producing exactly one further submission. It observes no
page errors. Existing table and durable modal cases also pass.

Selected Node 26.11.1/npm 12.2.0/Playwright 1.64.0 qualification: type-check,
lint, build/CSP and bundle budget pass. Commands and output are in adjacent logs.
The first combined code-health attempt correctly reports generated OpenAPI
staleness: the canonical inhibition validation change is coordinated with W06
for a single paired generation. Standalone raw transport and cancellation gates
pass; generated inventory documents require final refresh after all W09 source
is frozen. This receipt does not claim that pending combined gate passed or that
it replaces the parent-owned full integration suite.
