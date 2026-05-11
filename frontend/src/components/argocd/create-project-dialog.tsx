'use client';

// AppProject creation dialog. Captures only the fields most users need on day
// one — name, description, sourceRepos (one per line), and destinations
// (server / namespace pairs, one per line). The backend takes the rest of
// AppProjectSpec from defaults.

import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { X, Loader2, FolderTree } from 'lucide-react';
import { createArgoProject } from '@/lib/api';

interface CreateProjectDialogProps {
  instanceId: string;
  onClose: () => void;
}

interface DestinationRow {
  server: string;
  namespace: string;
}

function parseDestinations(text: string): DestinationRow[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean)
    .map((line) => {
      // Format: "<server>|<namespace>" or "<server> <namespace>"
      const parts = line.split(/\s*[|\s]\s*/);
      return {
        server: parts[0] ?? '',
        namespace: parts[1] ?? '*',
      };
    })
    .filter((d) => d.server);
}

function parseLines(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);
}

export function CreateProjectDialog({ instanceId, onClose }: CreateProjectDialogProps) {
  const queryClient = useQueryClient();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [sourceRepos, setSourceRepos] = useState('*');
  const [destinations, setDestinations] = useState('* | *');

  const create = useMutation({
    mutationFn: () =>
      createArgoProject(instanceId, {
        name: name.trim(),
        spec: {
          description: description.trim() || undefined,
          sourceRepos: parseLines(sourceRepos),
          destinations: parseDestinations(destinations),
        },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['argocd', 'projects', instanceId] });
      toast.success(`AppProject ${name} created`);
      onClose();
    },
    onError: (error: Error) => {
      toast.error(`Create failed: ${error.message}`);
    },
  });

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg rounded-xl border border-border bg-popover shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
              <FolderTree className="h-4 w-4 text-muted-foreground" />
            </div>
            <h3 className="text-lg font-semibold text-foreground">Create AppProject</h3>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="platform-team"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Owner: Platform Team"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Source Repos (one per line)</label>
            <textarea
              value={sourceRepos}
              onChange={(e) => setSourceRepos(e.target.value)}
              rows={3}
              placeholder="*&#10;https://github.com/org/*"
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              Glob patterns. <code>*</code> allows any.
            </p>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Destinations (one per line)</label>
            <textarea
              value={destinations}
              onChange={(e) => setDestinations(e.target.value)}
              rows={3}
              placeholder="* | *&#10;https://kubernetes.default.svc | production"
              className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <p className="text-xs text-muted-foreground">
              Format: <code>&lt;server&gt; | &lt;namespace&gt;</code>. Use <code>*</code> for any.
            </p>
          </div>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border bg-muted/30">
          <button
            onClick={onClose}
            disabled={create.isPending}
            className="inline-flex items-center h-8 px-3 rounded text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={() => create.mutate()}
            disabled={!name.trim() || create.isPending}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
              disabled:opacity-50"
          >
            {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create
          </button>
        </div>
      </div>
    </div>
  );
}
