import { Chrome, Github, KeyRound, Loader2, Plus, Shield } from 'lucide-react';
import { useSSOProviders } from '@/lib/hooks';
import { StatusBadge } from '@/components/ui/status-badge';

function providerIcon(type: string) {
  switch (type) {
    case 'github':
      return <Github className="h-5 w-5" />;
    case 'google':
      return <Chrome className="h-5 w-5" />;
    case 'oidc':
      return <KeyRound className="h-5 w-5" />;
    default:
      return <Shield className="h-5 w-5" />;
  }
}

export function SSOTab({ onAdd }: { onAdd: () => void }) {
  const { data: ssoProviders, isLoading: ssoLoading } = useSSOProviders();

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Configure Single Sign-On providers for your organization.
      </p>
      {ssoLoading ? (
        <div className="flex items-center justify-center h-32">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {(ssoProviders || []).map((provider) => (
            <div
              key={provider.id}
              className="flex items-center gap-4 p-4 rounded-lg border border-border bg-card hover:bg-card/80 transition-colors"
            >
              <div className="flex-shrink-0 w-10 h-10 rounded-lg bg-muted flex items-center justify-center text-muted-foreground">
                {providerIcon(provider.type)}
              </div>
              <div className="flex-1 min-w-0">
                <p className="font-medium text-foreground">{provider.name}</p>
                <p className="text-xs text-muted-foreground capitalize">{provider.type}</p>
              </div>
              <StatusBadge
                status={provider.enabled ? 'active' : 'disconnected'}
                label={provider.enabled ? 'Enabled' : 'Disabled'}
                size="sm"
              />
            </div>
          ))}

          <button
            type="button"
            onClick={onAdd}
            className="flex items-center justify-center gap-2 p-4 rounded-lg border border-dashed border-border
              text-muted-foreground hover:text-foreground hover:border-foreground/20 transition-colors"
          >
            <Plus className="h-4 w-4" />
            <span className="text-sm font-medium">Add SSO Provider</span>
          </button>
        </div>
      )}
    </div>
  );
}
