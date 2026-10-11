# Isolated downstream tool UIs

Version 2.0 serves downstream tool documents on separate HTTPS origins. Each
logical tool target (including its cluster, namespace and service where relevant)
has a stable `t-<digest>.<toolUI.domain>` hostname. Grafana remains private inside
the cluster; the public tool hostname reaches Astronomer's authorized tunnel
proxy, not an unauthenticated Grafana ingress.

## Configure DNS and TLS

Create a DNS-only wildcard A record for `*.tools.astronomer.example.com` pointing
to the same ingress address as the console. Do not put wildcard tool hosts into
the main `gateway.hosts` list: their entire path space must route to the server,
not the frontend. The chart creates the separate Ingress or HTTPRoute.

For Cloudflare, create an API token with **Zone:DNS:Edit** and **Zone:Zone:Read**,
restricted to the installation's zone. Store it in a Kubernetes Secret in the
Astronomer namespace under key `api-token`. Never commit the token or put it in
Helm values. Configure:

```yaml
config:
  serverURL: https://astronomer.example.com
toolUI:
  domain: tools.astronomer.example.com
tls:
  source: letsEncrypt
  letsEncrypt:
    email: operations@example.com
    cloudflare:
      apiTokenSecretName: astronomer-cloudflare-dns
      apiTokenSecretKey: api-token
```

cert-manager uses DNS-01 to issue and renew a certificate covering the console
and wildcard. HTTP-01 cannot issue wildcard certificates. Alternatively supply
a trusted wildcard certificate with `tls.source=secret`, or configure the
wildcard on an externally managed HTTPS Gateway (`tls.source=externalGateway`).
Self-signed mode is available for private environments with a trusted CA.

Empty `toolUI.domain` makes UI launch return 503 with configuration guidance.
Backend monitoring and tool management APIs remain available. There is no
fallback that serves downstream executable documents on the console origin.

## Browser and authorization boundary

- Existing sidebar and Grafana links are launch URLs. They return a trusted
  form that posts a 60-second, single-use grant to the specific tool host.
  Grants are not put in query strings, referrers or downstream requests.
- The tool host exchanges that grant for its own Secure, HttpOnly, host-only
  `__Host-astronomer_tool` cookie. The opaque encrypted session binds the parent
  browser session and exact target; it is not a control-plane bearer token.
- Every proxied request uses the existing JWT revocation/account checks and
  current RBAC. Generic service targets must match an enabled tool and its
  recorded installation namespace. Shared Grafana identity tickets are freshly
  issued, so an old sidecar cookie cannot retain a removed role.
- Tool hosts expose only the selected downstream application. Paths such as
  `/api/v1/auth/login` and `/internal` cannot select Astronomer's handlers.
  Browser credentials and user-supplied authorization are never forwarded to
  downstream services.
- Unsafe requests require the exact tool Origin. Tool-to-console CORS is
  denied even if a broader console CORS configuration would allow it.
- HTTPS console cookies use `__Host-` names, preventing sibling-domain cookie
  injection. HTTP-only local development retains its existing cookie names;
  isolated tool hosting requires HTTPS. Existing HTTPS users log in again
  after upgrade because the old unprefixed cookies are no longer accepted.
- Gateway-owned headers prevent downstream cookie, CORS, CSP and service-worker
  overrides. Service workers are disabled; blob workers remain available.
  Grafana frames additionally prohibit top-level navigation through sandboxing.

Tool sessions expire no later than the access token that launched them. After
expiry, reopen the tool from the console; ongoing background tool requests do
not extend the parent session. The configured suffix must share the console's registrable domain so
SameSite cookies work in embeds without third-party-cookie exceptions. Origins
and cookies remain separate; sharing the site does not share browser authority.

Grafana's old URL prefixes and deep-link queries are preserved on the isolated
host. Existing absolute console Grafana links in HTML and redirects are rewritten
at the gateway. Newly installed/upgraded Grafana releases receive the isolated
`root_url`; their internal ticket redemption URL remains the console URL.

## Validate

1. Resolve an arbitrary name under the wildcard to the ingress address.
2. Wait for the cert-manager Certificate to become Ready and verify its wildcard
   SAN. Confirm the wildcard route sends every path to `astronomer-server`.
3. Log in again, open shared and cluster Grafana, follow an Explore deep link,
   load dashboard assets and issue a datasource query.
4. Open another tool. Its hostname must differ from Grafana's and from another
   cluster's corresponding tool. Confirm cookies have no Domain attribute.
5. Revoke the session or tool permission and confirm subsequent requests fail.
6. Confirm the console proxy URL serves only the launch form, and attempting a
   control-plane URL on a tool host never returns a control-plane response.

References: [cert-manager Cloudflare DNS-01](https://cert-manager.io/docs/configuration/acme/dns01/cloudflare/),
[host-only cookie prefixes](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie),
[origin-keyed browser contexts](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Origin-Agent-Cluster).
