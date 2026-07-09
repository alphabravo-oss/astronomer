# Browser session lifecycle

Astronomer uses signed JWTs for interactive browser/API sessions. The access
token has an **absolute lifetime of 60 minutes by default**. Activity does not
extend that token and the value is not an idle or sliding timeout.

Operators can set `session.timeout_minutes` from **Settings → Platform →
Browser session**. The accepted range is 5–10,080 minutes. Password login,
refresh, SSO callback, TOTP verification, and forced TOTP enrollment completion
all read the same setting when issuing an access token. Refresh tokens remain
separately bounded and may issue a new access token using the current setting.

If the setting has never been written, Astronomer uses 60 minutes. If stored
data is malformed or outside the accepted range, the server logs the setting
key and bounds, uses the safe 60-minute default, and continues serving. This
prevents corrupt settings data from producing an unbounded session.

The boot-time `SESSION_TIMEOUT_MINUTES` value seeds the JWT manager for startup
and degraded paths. Values outside the same 5–10,080 minute range fall back to
60 minutes. The runtime platform setting is authoritative for subsequent
interactive access-token mints.
