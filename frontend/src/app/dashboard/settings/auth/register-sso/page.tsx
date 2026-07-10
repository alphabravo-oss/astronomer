'use client';

/**
 * /dashboard/settings/auth/register-sso/ — one-click "wire Dex as our SSO".
 *
 * Mechanically this just POSTs /register-as-sso/ which creates (or updates)
 * the `sso_configurations` row pointing at our installed Dex. The Phase A1
 * OIDC discovery path then picks Dex up like any other OIDC IdP on the next
 * login.
 *
 * The form has three inputs:
 *   - Client ID (defaults to "astronomer")
 *   - Client secret (must match a `staticClients[*].secret` configured in
 *     Dex; if you flip the client to public, leave blank)
 *   - Display name (text on the login button)
 */
import { useEffect, useState } from 'react';
import { Link } from '@/lib/link';
import { ArrowLeft, ExternalLink, Loader2, ShieldCheck } from 'lucide-react';
import { useDexSettings, useRegisterDexAsSSO } from '@/components/auth/hooks';

export default function RegisterAsSSOPage() {
  const { data: settings } = useDexSettings();
  const registerMutation = useRegisterDexAsSSO();

  const [clientId, setClientId] = useState('astronomer');
  const [clientSecret, setClientSecret] = useState('');
  const [displayName, setDisplayName] = useState('Sign in with Dex');
  const [success, setSuccess] = useState<{ provider: string; issuerUrl: string } | null>(null);

  useEffect(() => {
    if (success) return; // don't snap state back after a successful submit
    if (settings?.publicClients?.[0]?.id) {
      setClientId(settings.publicClients[0].id);
    }
    if (settings?.publicClients?.[0]?.name) {
      setDisplayName(`Sign in with ${settings.publicClients[0].name}`);
    }
  }, [settings, success]);

  const handleSubmit = async () => {
    try {
      const res = await registerMutation.mutateAsync({
        client_id: clientId,
        client_secret: clientSecret || undefined,
        display_name: displayName,
      });
      if (!res.verified || !res.secretResourceVersion) {
        throw new Error('Dex rollout verification was not returned by the server');
      }
      setSuccess({ provider: res.provider, issuerUrl: res.issuerUrl });
    } catch {
      /* mutation toasts on error */
    }
  };

  const issuerConfigured = !!settings?.issuerUrl;

  return (
    <div className="max-w-2xl mx-auto space-y-6">
      <Link
        href="/dashboard/settings/auth"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to Auth
      </Link>

      <div>
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          Auth · Register SSO
        </p>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
          Register Dex as the platform&apos;s SSO provider
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Astronomer&apos;s OIDC SSO manager (Phase A1) will treat Dex like any other
          upstream OIDC IdP. After this, your login screen shows the &ldquo;{displayName}&rdquo; button
          and Dex handles the upstream IdP fan-out.
        </p>
      </div>

      {!issuerConfigured && (
        <div className="rounded-lg border border-status-warning/40 bg-status-warning/5 p-3 flex items-start gap-2">
          <ShieldCheck className="h-4 w-4 text-status-warning flex-shrink-0 mt-0.5" />
          <div className="text-xs text-status-warning/90 space-y-1">
            <p className="font-medium">Issuer URL not set.</p>
            <p>
              Configure Dex settings before registering it as SSO — the SSO row needs to
              know where to discover OIDC metadata.
            </p>
            <Link
              href="/dashboard/settings/auth/settings"
              className="inline-flex items-center gap-1 underline hover:no-underline"
            >
              Open Dex Settings
              <ExternalLink className="h-3 w-3" />
            </Link>
          </div>
        </div>
      )}

      {success ? (
        <div className="rounded-xl border border-status-success/40 bg-status-success/5 p-5 space-y-3">
          <div className="flex items-center gap-2">
            <ShieldCheck className="h-5 w-5 text-status-success" />
            <p className="text-sm font-semibold text-foreground">Dex is now your SSO provider.</p>
          </div>
          <p className="text-sm text-muted-foreground">
            The <span className="font-mono text-xs">{success.provider}</span> row in
            <span className="font-mono text-xs"> sso_configurations</span> is enabled and
            pointed at <span className="font-mono text-xs">{success.issuerUrl}</span>.
          </p>
          <p className="text-sm text-muted-foreground">
            Now try logging out and back in — you should see the &ldquo;{displayName}&rdquo;
            button on the login screen.
          </p>
          <div className="flex items-center gap-2 pt-2">
            <Link
              href="/dashboard/settings/auth"
              className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              Back to Auth
            </Link>
            <button
              type="button"
              onClick={() => setSuccess(null)}
              className="inline-flex items-center h-9 px-3 rounded-lg text-sm text-muted-foreground hover:text-foreground transition-colors"
            >
              Edit again
            </button>
          </div>
        </div>
      ) : (
        <div className="rounded-xl border border-border bg-card p-5 space-y-4">
          <FieldRow label="Dex client ID" required helper="Defaults to `astronomer`. Must match a `staticClients[*].id` configured in Dex.">
            <input
              type="text"
              value={clientId}
              onChange={(e) => setClientId(e.target.value)}
              placeholder="astronomer"
              className={inputCls}
            />
          </FieldRow>

          <FieldRow
            label="Dex client secret"
            helper="Match the `staticClients[*].secret` you set under Dex Settings → Static clients. Leave blank for public clients."
          >
            <input
              type="password"
              value={clientSecret}
              onChange={(e) => setClientSecret(e.target.value)}
              placeholder="••••••••"
              autoComplete="new-password"
              className={inputCls}
            />
          </FieldRow>

          <FieldRow label="Display name" helper="Shown on the platform's login button.">
            <input
              type="text"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="Sign in with Dex"
              className={inputCls}
            />
          </FieldRow>

          <div className="rounded-lg border border-border bg-background p-3 text-xs text-muted-foreground space-y-1">
            <p>
              <span className="font-medium text-foreground">Heads up:</span> the client
              secret you enter here is encrypted at rest. To actually let users in, the
              same secret must be present in Dex&apos;s <span className="font-mono">staticClients</span> config —
              configure it under{' '}
              <Link href="/dashboard/settings/auth/settings" className="underline hover:no-underline">
                Dex Settings
              </Link>{' '}
              and Apply.
            </p>
          </div>

          <div className="flex items-center justify-end gap-2 pt-2">
            <Link
              href="/dashboard/settings/auth"
              className="h-9 px-4 rounded-lg border border-border text-sm font-medium
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors inline-flex items-center"
            >
              Cancel
            </Link>
            <button
              type="button"
              onClick={handleSubmit}
              disabled={registerMutation.isPending || !issuerConfigured || !clientId.trim()}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {registerMutation.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Register
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

const inputCls =
  'w-full h-10 px-3 rounded-lg border border-border bg-background text-sm placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring';

function FieldRow({
  label,
  helper,
  required,
  children,
}: {
  label: string;
  helper?: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-error ml-0.5">*</span>}
      </label>
      {children}
      {helper && <p className="text-2xs text-muted-foreground">{helper}</p>}
    </div>
  );
}
