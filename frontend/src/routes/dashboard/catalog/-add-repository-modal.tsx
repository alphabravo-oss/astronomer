import { useState } from 'react';
import { ActionButton } from '@/components/ui/action-button';
import { Input } from '@/components/ui/input';
import { ModalShell } from '@/components/ui/modal-shell';
import { useAppForm, useStore } from '@/lib/form';
import { useCreateHelmRepository } from '@/lib/hooks';
import { cn } from '@/lib/utils';
import type { HelmRepoType } from '@/types';
import { ChevronDown } from 'lucide-react';

export function AddRepositoryModal({ onClose }: { onClose: () => void }) {
  const createRepo = useCreateHelmRepository();
  const form = useAppForm({
    defaultValues: {
      name: '',
      url: '',
      repoType: 'helm' as HelmRepoType,
      description: '',
      username: '',
      password: '',
    },
    onSubmit: async ({ value }) => {
      try {
        await createRepo.mutateAsync({
          name: value.name,
          url: value.url,
          repoType: value.repoType,
          description: value.description || undefined,
          username: value.username || undefined,
          password: value.password || undefined,
        });
        onClose();
      } catch {
        // Error handled by mutation
      }
    },
  });
  const [showAuth, setShowAuth] = useState(false);

  const repoName = useStore(form.store, (s) => s.values.name);
  const repoUrl = useStore(form.store, (s) => s.values.url);
  const repoType = useStore(form.store, (s) => s.values.repoType);

  const footer = (
    <>
      <ActionButton onClick={onClose}>Cancel</ActionButton>
      <ActionButton
        intent="primary"
        loading={createRepo.isPending}
        disabled={!repoName || !repoUrl}
        onClick={() => void form.handleSubmit()}
      >
        Add Repository
      </ActionButton>
    </>
  );

  return (
    <ModalShell
      title="Add Repository"
      onClose={onClose}
      size="md"
      footer={footer}
      footerClassName="flex items-center justify-end gap-2"
    >
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Name</label>
        <form.Field name="name">
          {(field) => (
            <Input
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="prometheus-community"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">URL</label>
        <form.Field name="url">
          {(field) => (
            <Input
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="https://prometheus-community.github.io/helm-charts"
              className="font-mono"
            />
          )}
        </form.Field>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Type</label>
        <div className="flex gap-1.5">
          {(['helm', 'oci'] as const).map((type) => (
            <button
              key={type}
              onClick={() => form.setFieldValue('repoType', type)}
              className={cn(
                'px-4 py-1.5 rounded-md text-xs font-medium transition-colors uppercase',
                repoType === type
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground hover:text-foreground',
              )}
            >
              {type}
            </button>
          ))}
        </div>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Description</label>
        <form.Field name="description">
          {(field) => (
            <Input
              type="text"
              value={field.state.value}
              onChange={(e) => field.handleChange(e.target.value)}
              onBlur={field.handleBlur}
              placeholder="Optional description"
            />
          )}
        </form.Field>
      </div>

      <button
        onClick={() => setShowAuth(!showAuth)}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors"
      >
        <ChevronDown className={cn('h-4 w-4 transition-transform', showAuth && 'rotate-180')} />
        Authentication (optional)
      </button>

      {showAuth && (
        <div className="space-y-4 pl-4 border-l-2 border-border">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Username</label>
            <form.Field name="username">
              {(field) => (
                <Input
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="Username"
                />
              )}
            </form.Field>
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Password</label>
            <form.Field name="password">
              {(field) => (
                <Input
                  type="password"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="Password or token"
                />
              )}
            </form.Field>
          </div>
        </div>
      )}
    </ModalShell>
  );
}
