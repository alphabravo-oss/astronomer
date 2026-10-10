# Constructor authority for finite redirects

The canonical destination client accepts up to eight directed HTTPS origin pairs
through `DestinationOptions.RedirectOriginPairs`. This constructor option grants
no destination authority itself: both origins must already be explicit entries
in the independently supplied destination policy, including their approved DNS
address ranges. Unrestricted public defaults alone do not satisfy this rule.
Catalog documents, signed manifests and release archives cannot select this
option. Runtime distribution and S3 composition retain their strict defaults.

An opted-in client permits at most two total redirect hops; callers can reduce
that bound to one. Each cross-origin transition must match a directed pair.
Every request, redirect and connection still checks the canonical policy, rejects
any DNS answer outside its ranges, denies metadata and loopback addresses and
connects to a checked IP. The legacy public resolver option cannot be combined
with finite pairs. Its separately reviewed public-only behavior remains intact.

Authorization, proxy authorization, cookies and configured sensitive headers
are removed whenever an origin changes. They also remain absent on subsequent
hops, because Go can otherwise copy custom headers from the original request
onto a later request. Range headers survive the transition. Pair, policy and
sensitive-header inputs are frozen at construction.

The actual supplier prerequisite was a denied GHCR redirect to
`https://pkg-containers.githubusercontent.com`, retained in
[the range prerequisite](trivy-range-prerequisite.json).
GitHub lists both domains as package and container download destinations in its
[official runner documentation](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).
This attribution does not authenticate payloads or grant network permission.
An operator tool may construct that exact pair only with both approved origins
and their independently checked ranges. A one-byte range response, when tested,
will establish range routing only. Full supplier bytes, whole-layer SHA256,
authenticated original signatures and database freshness remain separate checks.

The TLS fixture uses controlled local servers, logical approved origins and an
injected checked-IP dialer. It exercises two redirect hops, credential stripping
and Range preservation; a third hop, an unapproved origin, mixed DNS answers and
DNS changes between validation and connection must fail. Separate cases retain
metadata denial even with broad explicit CIDRs. These fixtures do not claim
production OIDC, public supplier delivery or a deployed disconnected environment.
