# Peer redirects and webhook response status

This packet retains the actual baseline, corrected race run, original fixture
failure, and Go vet outcome for eleven source files over application commit
`27102246b69ee4fbd3f7d5ed1a06119ef02d8fb0`. `index.json` binds each corrected
file and each compressed original; gzip decompression preserves the exact
original receipt and log bytes.

Authenticated cross-pod HTTP clients now refuse automatic redirects. Kubernetes
and Helm requests retain their signed payload at the locator-selected peer;
the proxy retains bearer/cookie credentials there. Normal peer forwarding still
works. The proxy's streamed request body already prevents replay for 307/308;
those non-followed responses retain the existing status/Location passthrough.

Webhook sends reject HTTP response statuses outside 100–999 before reading the
body or narrowing to the database's int32 status. The dispatcher independently
checks the outcome before recording delivered, retried or dropped attempts.
Invalid values become status zero with a diagnostic and retain ordinary retry
and exhaustion behavior. A real TLS HTTP/2 fixture sends `:status=4294967496`;
the baseline accepts it, while the corrected sender rejects it. This is separate
from the fake-doer and fake-queue regression controls.

The baseline has 39 failing test results across five parents. Its two proxy
307/308 assertions incorrectly required discarding the original response, so
those failures are not additional automatic-redirect defects. The first
corrected race run retains 69 passing and three failing results: those two
assertions and their parent. A test-only correction preserves the response
contract. The final run passes all 72 results across 31 parents with no skips,
including normal forwarding, signing, timeout, 4xx/5xx and retry controls.
All owned child groups were held and drained; unchanged default 20 GiB and
20 percent capacity guards passed. Full internal/cmd Go vet passed. The original
complexity failure is retained; later repository/frontend checks have separate
scope and receipts.

This repairs two bounded runtime behaviors. It does not establish exploit
severity, accept every static finding, qualify the whole source security policy,
or prove PostgreSQL, a signed candidate, cluster deployment or publication.

Integrated frontend checks pass on the recorded source overlay: all 350 test
files and 2,444 tests, TypeScript, lint, production build, CSP and unchanged
bundle budgets. Canonical route generation and seven server authentication/CSRF
parents pass with 244 named results. The integration-check entries bind retained
raw outputs and successful receipts; earlier dependency, inventory and lint
failures are preserved. The first dependency attempt used bundled npm 11.20.0;
the corrected lockfile install and checks use npm 12.2.0 and Node 26.11.1.
The frontend build is a production-mode bundle with no release source stamp,
image signature, authenticated browser or deployment claim.
