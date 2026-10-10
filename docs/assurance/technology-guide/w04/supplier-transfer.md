# Bounded supplier layer transfer

This reviewed source implements original supplier layer transfer before
endorsement. It was applied from the retained external review patch
`f8229e7bdb30411132d794d7ef6324e57063456c0e977f13ddb137b471e86391`.
It has not yet been built or tested. Earlier corrected and failed W04 packets
retain their original source and qualification scopes.

The constructor-bound capture supplies one exact approved repository and fixed
role layer. Transfer validates its owned destination policy again, refuses an
existing output, pins a private owned directory with no-follow/nonblocking open,
checks finite capacity for the new layer plus a 512-MiB reserve, and creates only
private regular staging. One new layer is at most two GiB; the output is a hard
link to the same staged inode rather than an additional payload allocation.
Free capacity already subtracts existing filesystem contents; the guard reserves
only newly staged bytes and headroom. The
separate complete kit producer must still enforce its eight-GiB aggregate policy.
Qualification jobs also need the maintained host capacity guard independently.

Supplier and signature-first children share the existing bounded Range reader:
64-MiB chunks, at most 32, exact single 206 Content-Range start/end/total, exact
body bytes, no encoding/multipart, whole SHA256 and a fifteen-minute deadline.
No transfer data chooses a destination, credential or redirect policy. The
supplier function establishes content only and cannot return vendor Verification
or an authenticated feed. The signed path keeps canonical signature acceptance
before reaching this shared exact-byte reader.

After complete download, the file is synced and inspected with the same two-pass
hash-before-interpretation archive core. Actual metadata must pass the owned
schema/clock/age policy. Only then can a new output be hard-linked, checked against
the verified descriptor's inode/size/mtime, the owned staging link removed, and
the directory synced and rechecked. Failure cleanup removes only paths still
matching owned original file identity; same-user path replacements are preserved.
A privileged or trusted same-user host can mutate filesystem paths after any
check. Later child endorsement, feed publishing and all consumers must reverify
original content and current trust/state rather than trust existence of output.

Source fixtures exercise original new unsigned main/Java downloads and no
fabricated feed, exact failed ranges/body hashes, stale actual metadata after
complete bytes, no layer request for refused constructor/host/output conditions,
cancellation after staged bytes, existing-output preservation, owned staging
replacement preservation and normal cleanup. Tiny QA payloads do not qualify
actual supplier sizes, archive expansion, transport/CDN availability or cadence.

No actual transfer, supplier measurement, publisher execution, durable update,
scan job or disconnected cluster is claimed. Whole W04 remains in progress.


## Registry challenge boundary (unqualified source)

The original draft's direct `http.Client.Do` range path did not inherit ORAS's
anonymous bearer token obtained during metadata capture. That is a concrete
protocol gap, not a supplier payload failure. Root observed the public GHCR main
schema-tag HEAD return HTTP 401 at 2026-10-10T03:02:42Z, advertising the same-origin
`https://ghcr.io/token` realm, `ghcr.io` service and the exact main repository pull
scope. No token was requested or retained in that read-only observation.
[GitHub's Container registry documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
states public images support anonymous access; that does not promise a
challenge-free HTTP transaction.

`DatabaseRegistryAuthentication` belongs only to the operator constructor. Empty
policy permits anonymous same-registry HTTPS realm and service. Separate token
origin or username/password needs an explicit exact realm URL and service; it
cannot grant network access. `NewDatabaseRegistryClient` copies caller values,
clears ambient cookie jars, owns a repository-local ORAS cache and replaces caller
scope hints with that repository's pull scope. The constructor wraps the same
original guarded transport rather than creating a default external client.
The signed child path still verifies the original vendor signature and descriptor
before this range-transfer core; supplier proof remains transport/content only.

Request ordering is: fixed repository outer adapter, ORAS bearer machinery, cloned
bounded guarded HTTP client, challenge/token middleware, original canonical
transport. A 401's single finite bearer challenge must bind the exact repository
pull scope, reviewed realm and service before ORAS may make a token request.
Private Basic credentials may reach only that exact token endpoint. The inner
client rejects all token redirects and forwards only finite guarded blob
redirects; its original redirect callback retains credential stripping and DNS/IP
pinning. Every token origin still must pass the canonical destination policy.
No token, password, raw challenge, signed Location or query is returned in an
adapter error. Unsupported Basic registry challenges, OAuth refresh flows,
multiple challenges, escaped challenge parameters or expanded scopes fail closed.

Each request is bounded to one minute (or the caller's shorter timeout); complete
transfer remains bounded to fifteen minutes. A token response is at most 128 KiB,
with strict duplicate/unknown/depth/trailing-document rejection through
`jsoncontract.Decode`, selected standard token fields and each credential at most
16 KiB. Both transport wrappers forward `CloseIdleConnections`.
The new exact JSON reader site is `resolver.boundDatabaseTokenResponse`; its
source ownership must be annotated in the combined W09 inventory after reviewed
integration. No existing parser inventory baseline was refreshed.

Unexecuted owned TLS peer fixtures characterize anonymous and configured private
401 → bounded token → two exact immutable ranges, no mutable-tag requests,
repository-local token reuse, realm/service/scope expansion, duplicate challenges,
redirected/duplicate/trailing/oversized token bodies, ambient credentials,
cancellation and a configured token origin denied by the underlying transport.
The fixture transport routes only literal test origins to the owned TLS peer.
It is not supplier DNS/private-CA qualification, production OIDC publication,
actual whole supplier transfer, freshness acceptance or a real scan-used witness.
