'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import {
  useProjects,
  useCreateProject,
  useDeleteProject,
  useClusters,
  useClusterNamespaces,
} from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { Project } from '@/types';
import {
  FolderKanban,
  Plus,
  Trash2,
  X,
  Loader2,
  Users,
} from 'lucide-react';
import { toast } from 'sonner';

export default function ProjectsPage() {
  const router = useRouter();
  const [showCreateModal, setShowCreateModal] = useState(false);

  const { data: projectsData, isLoading: projectsLoading } = useProjects();
  const deleteProject = useDeleteProject();

  const projects = projectsData?.data || [];

  const projectColumns: Column<Project>[] = [
    {
      key: 'name',
      header: 'Project',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <FolderKanban className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium text-foreground">{row.displayName}</p>
            <p className="text-xs text-muted-foreground font-mono">{row.name}</p>
          </div>
        </div>
      ),
    },
    {
      key: 'description',
      header: 'Description',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground truncate max-w-[300px] block">
          {row.description || '--'}
        </span>
      ),
      sortable: false,
    },
    {
      key: 'namespaces',
      header: 'Namespaces',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.namespaces.length === 0 ? (
            <span className="text-xs text-muted-foreground">None</span>
          ) : (
            <>
              {row.namespaces.slice(0, 3).map((ns) => (
                <span key={ns} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
                  {ns}
                </span>
              ))}
              {row.namespaces.length > 3 && (
                <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
                  +{row.namespaces.length - 3}
                </span>
              )}
            </>
          )}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'members',
      header: 'Members',
      accessor: (row) => (
        <div className="flex items-center gap-1.5">
          <Users className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="tabular-nums text-sm">{row.members.length}</span>
        </div>
      ),
      sortAccessor: (row) => row.members.length,
      align: 'center',
    },
    {
      key: 'resourceQuota',
      header: 'Resource Quota',
      accessor: (row) => {
        if (!row.resourceQuota) {
          return <span className="text-xs text-muted-foreground">No quota</span>;
        }
        return (
          <div className="flex flex-wrap gap-1">
            <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              CPU: {row.resourceQuota.cpuLimit}
            </span>
            <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              Mem: {row.resourceQuota.memoryLimit}
            </span>
          </div>
        );
      },
      sortable: false,
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => {
              if (confirm('Delete this project? This action cannot be undone.')) {
                deleteProject.mutate(row.id);
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete project"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Projects</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Organize clusters and namespaces into logical projects
          </p>
        </div>
        <button
          onClick={() => setShowCreateModal(true)}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
            text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          Create Project
        </button>
      </div>

      {/* Projects Table */}
      <DataTable
        data={projects}
        columns={projectColumns}
        keyExtractor={(row) => row.id}
        searchPlaceholder="Search projects..."
        loading={projectsLoading}
        emptyMessage="No projects created yet"
        onRowClick={(row) => router.push(`/dashboard/projects/${row.id}`)}
      />

      {/* Create Project Modal */}
      {showCreateModal && (
        <CreateProjectModal onClose={() => setShowCreateModal(false)} />
      )}
    </div>
  );
}

// ============================================================
// Create Project Modal
// ============================================================

function CreateProjectModal({ onClose }: { onClose: () => void }) {
  const createProject = useCreateProject();
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const clusters = clustersData?.data || [];

  const [form, setForm] = useState({
    name: '',
    displayName: '',
    description: '',
    clusterId: '',
    namespaces: [] as string[],
  });

  const { data: namespacesData } = useClusterNamespaces(form.clusterId);
  const namespaces = namespacesData || [];

  const toggleNamespace = (ns: string) => {
    setForm((f) => ({
      ...f,
      namespaces: f.namespaces.includes(ns)
        ? f.namespaces.filter((n) => n !== ns)
        : [...f.namespaces, ns],
    }));
  };

  const handleSave = async () => {
    if (!form.name || !form.displayName) {
      toast.error('Name and display name are required');
      return;
    }

    try {
      await createProject.mutateAsync({
        name: form.name,
        displayName: form.displayName,
        description: form.description || undefined,
        clusterIds: form.clusterId ? [form.clusterId] : [],
        namespaces: form.namespaces,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Create Project</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-') }))}
                placeholder="project-name"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Display Name</label>
              <input
                type="text"
                value={form.displayName}
                onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
                placeholder="My Project"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <input
              type="text"
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="Describe this project's purpose"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Cluster</label>
            <select
              value={form.clusterId}
              onChange={(e) => setForm((f) => ({ ...f, clusterId: e.target.value, namespaces: [] }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">Select a cluster</option>
              {clusters.map((cluster) => (
                <option key={cluster.id} value={cluster.id}>
                  {cluster.displayName}
                </option>
              ))}
            </select>
          </div>

          {/* Namespaces */}
          {form.clusterId && (
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Namespaces</label>
              <div className="flex flex-wrap gap-1.5 max-h-40 overflow-y-auto p-2 rounded-md border border-border bg-background">
                {namespaces.length === 0 ? (
                  <span className="text-xs text-muted-foreground">Loading namespaces...</span>
                ) : (
                  namespaces.map((ns) => (
                    <button
                      key={ns.name}
                      onClick={() => toggleNamespace(ns.name)}
                      className={cn(
                        'px-2.5 py-1 rounded text-xs font-medium transition-colors',
                        form.namespaces.includes(ns.name)
                          ? 'bg-primary text-primary-foreground'
                          : 'bg-muted text-muted-foreground hover:text-foreground'
                      )}
                    >
                      {ns.name}
                    </button>
                  ))
                )}
              </div>
              <p className="text-xs text-muted-foreground">
                {form.namespaces.length} namespace{form.namespaces.length !== 1 ? 's' : ''} selected
              </p>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={createProject.isPending || !form.name || !form.displayName}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createProject.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create Project
          </button>
        </div>
      </div>
    </div>
  );
}
