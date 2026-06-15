'use client';

// Wizard page 3 - live progress timeline. Subscribes to the wizard's
// per-cluster SSE stream and renders one row per step via the shared
// RegistrationTimeline component (sprint 23). When the cluster reaches
// `ready`, the "Take me to the cluster" CTA appears.

import { useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import { Check, Server } from 'lucide-react';
import { RegistrationTimeline } from '@/components/clusters/registration-timeline';

export default function ProgressStepPage() {
  const router = useRouter();
  const params = useParams();
  const clusterId = String(params?.id ?? '');
  const [isReady, setIsReady] = useState(false);

  return (
    <div className="max-w-3xl mx-auto p-6">
      <div className="mb-6 flex items-center gap-3">
        <div className="w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
          <Server className="h-5 w-5 text-muted-foreground" />
        </div>
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Adoption progress</h1>
          <p className="text-sm text-muted-foreground">
            Step 3 of 3 - Watch the existing cluster connect and apply its baseline
          </p>
        </div>
      </div>

      <RegistrationTimeline clusterId={clusterId} onReady={() => setIsReady(true)} />

      {isReady && (
        <div className="mt-6 flex items-center justify-between p-4 rounded-lg border border-status-success/30 bg-status-success/5">
          <div className="flex items-center gap-2 text-sm font-medium text-status-success">
            <Check className="h-4 w-4" />
            Cluster is ready
          </div>
          <button
            onClick={() => router.push(`/dashboard/clusters/${clusterId}`)}
            className="h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90"
          >
            Take me to the cluster
          </button>
        </div>
      )}
    </div>
  );
}
