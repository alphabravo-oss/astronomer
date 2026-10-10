# Operator registry tool redirect policy

Publication, mirror execution and offline export compose their HTTP client
through `registry_tool_client.go`. This constructor enables the single directed
HTTPS transition from `ghcr.io` to `pkg-containers.githubusercontent.com` only
when the independently supplied operator destination policy explicitly approves
both default-port origins and their DNS ranges. Public defaults, approval of
only one origin, other hosts or other ports retain the strict redirect default.
Case and an explicit port 443 normalize to the same origin. Invalid policy
entries fail before a client is returned.

The canonical client retains its two-hop limit, credential stripping on every
hop after an origin change, all-answer DNS validation, checked-IP dial policy,
TLS verification, finite timeout and existing response limits. This adapter does
not change runtime distribution, catalogue or S3 composition, accept redirect
origins from signed content, or allow ambient credential configuration.

The [canonical redirect receipt](finite-redirect-evidence.json) and
[one-byte supplier probe](finite-range-probe.json) establish their original,
separate scopes. The latter proves range routing, not full database bytes,
whole-layer SHA256, authenticated vendor publication or database freshness.
This CLI source must receive its own coupled qualification after the accepted
mainline's stricter JSON reader is incorporated. The task map records the
remaining [W03 criteria](task-status.md).
