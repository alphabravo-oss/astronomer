# Pinned Charlie Product Bridge contract

This is Astronomer's sole Charlie wire-contract area. `pinned/` is the reviewed
Charlie v1 contract snapshot identified by `pin.json` and `checksums.sha256`.
Schema JSON may be formatting-normalized without semantic changes. Change it only through a deliberate contract update that
regenerates the client and updates every checksum.

The generated transport lives below `internal/wire`, which Go permits only this
contract tree to import. Other Astronomer packages use `NewLocalClient` and can
therefore target only:

```text
https://charlie-agent-bridge.<namespace>.svc:7443/bridge/v1
```

No browser, handler, worker, or other package may create a Charlie central HTTP
client. The bridge constructor requires backend-owned feature availability and
product-owned mutual TLS, and accepts no custom host or URL.

Generation and drift checks:

```sh
make charlie-contract-generate
make charlie-contract-check
```

The fake bridge under `fakebridge/` is deterministic contract infrastructure.
It does not contain credentials, prompts, evidence, or product payloads.

The pinned connector-capability and Kubernetes-visibility schemas define the
optional product-owned connector boundary. They never authorize Astronomer to
call Charlie Central directly or give the generic agent Kubernetes credentials.

`pinned/live-qualification-scenarios-v1.json` is also the sole scenario,
assertion, and timeout catalog for the operator-started live qualification
hook. `QualificationScenarioContract` strictly decodes that reviewed artifact;
Astronomer must not maintain a parallel hand-written scenario map.
