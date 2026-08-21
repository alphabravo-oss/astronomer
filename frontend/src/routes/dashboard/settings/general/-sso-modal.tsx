import { useCreateSSOProvider } from '@/lib/hooks';
import { useAppForm, useStore } from '@/lib/form';
import { toastError } from '@/lib/toast';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { Select } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';

export function SSOModal({ onClose }: { onClose: () => void }) {
  const createSSOProvider = useCreateSSOProvider();

  const ssoForm = useAppForm({
    defaultValues: {
      type: 'github' as 'github' | 'google' | 'oidc',
      name: '',
      clientId: '',
      clientSecret: '',
      metadataUrl: '',
      allowedOrganizations: '',
      autoCreateUsers: true,
    },
    validators: {
      // Old checks (imperative, pre-submit): `if (!ssoForm.name)` then
      // `if (!ssoForm.clientId)` → ported 1:1 as a form-level onSubmit
      // validator; same messages, same order.
      onSubmit: ({ value }) =>
        !value.name
          ? 'Provider name is required'
          : !value.clientId
            ? 'Client ID is required'
            : undefined,
    },
    // Same UX as before: the failed check surfaces as a toast, not inline.
    onSubmitInvalid: ({ formApi }) => {
      const err = formApi.state.errors.find((e) => typeof e === 'string');
      if (err) toastError(err);
    },
    onSubmit: async ({ value }) => {
      try {
        await createSSOProvider.mutateAsync({
          type: value.type,
          name: value.name,
          enabled: true,
          config: {
            clientId: value.clientId,
            clientSecret: value.clientSecret || undefined,
            metadataUrl: value.metadataUrl || undefined,
            allowedOrganizations: value.allowedOrganizations || undefined,
            autoCreateUsers: value.autoCreateUsers,
          },
        });
        onClose();
        ssoForm.reset();
      } catch {
        // Error is handled by the mutation's onError callback
      }
    },
  });
  // Old disabled gate (`!ssoForm.name || !ssoForm.clientId`), recomputed from
  // form state; the OIDC discovery field is conditional on the type value.
  const ssoType = useStore(ssoForm.store, (s) => s.values.type);
  const ssoName = useStore(ssoForm.store, (s) => s.values.name);
  const ssoClientId = useStore(ssoForm.store, (s) => s.values.clientId);

  return (
    <ModalShell
      title="Add SSO Provider"
      onClose={onClose}
      size="sm"
      footer={
        <>
          <ActionButton onClick={onClose}>Cancel</ActionButton>
          <ActionButton
            intent="primary"
            onClick={() => void ssoForm.handleSubmit()}
            disabled={!ssoName || !ssoClientId}
            loading={createSSOProvider.isPending}
          >
            Add Provider
          </ActionButton>
        </>
      }
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="space-y-4">
        <div className="space-y-1.5">
          <label htmlFor="sso-provider-type" className="text-sm font-medium text-foreground">Provider Type</label>
          <ssoForm.Field name="type">
            {(field) => (
              <Select
                id="sso-provider-type"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value as 'github' | 'google' | 'oidc')}
                onBlur={field.handleBlur}
              >
                <option value="github">GitHub</option>
                <option value="google">Google</option>
                <option value="oidc">OIDC</option>
              </Select>
            )}
          </ssoForm.Field>
        </div>

        <div className="space-y-1.5">
          <label htmlFor="sso-provider-name" className="text-sm font-medium text-foreground">Provider Name</label>
          <ssoForm.Field name="name">
            {(field) => (
              <Input
                id="sso-provider-name"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="e.g., Corporate GitHub"
                autoFocus
              />
            )}
          </ssoForm.Field>
        </div>

        <div className="space-y-1.5">
          <label htmlFor="sso-client-id" className="text-sm font-medium text-foreground">Client ID</label>
          <ssoForm.Field name="clientId">
            {(field) => (
              <Input
                id="sso-client-id"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="OAuth client ID"
              />
            )}
          </ssoForm.Field>
        </div>

        <div className="space-y-1.5">
          <label htmlFor="sso-client-secret" className="text-sm font-medium text-foreground">Client Secret</label>
          <ssoForm.Field name="clientSecret">
            {(field) => (
              <Input
                id="sso-client-secret"
                type="password"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="OAuth client secret"
              />
            )}
          </ssoForm.Field>
        </div>

        {ssoType === 'oidc' && (
          <div className="space-y-1.5">
            <label htmlFor="sso-discovery-url" className="text-sm font-medium text-foreground">Discovery URL</label>
            <ssoForm.Field name="metadataUrl">
              {(field) => (
                <Input
                  id="sso-discovery-url"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="https://idp.example.com/.well-known/openid-configuration"
                />
              )}
            </ssoForm.Field>
          </div>
        )}

        <div className="space-y-1.5">
          <label htmlFor="sso-allowed-orgs" className="text-sm font-medium text-foreground">Allowed Organizations</label>
          <ssoForm.Field name="allowedOrganizations">
            {(field) => (
              <Input
                id="sso-allowed-orgs"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="Comma-separated list (optional)"
              />
            )}
          </ssoForm.Field>
        </div>

        <div className="flex items-center justify-between p-3 rounded-lg border border-border">
          <div>
            <p className="text-sm font-medium text-foreground">Auto-create Users</p>
            <p className="text-xs text-muted-foreground">Automatically create accounts on first login</p>
          </div>
          <ssoForm.Field name="autoCreateUsers">
            {(field) => (
              <Switch
                checked={field.state.value}
                onCheckedChange={field.handleChange}
                onBlur={field.handleBlur}
              />
            )}
          </ssoForm.Field>
        </div>
      </div>
    </ModalShell>
  );
}
