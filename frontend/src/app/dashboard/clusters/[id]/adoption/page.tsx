'use client';

// Cluster adoption timeline on cluster detail.
//
// Mirrors the registration wizard's progress page but uses product language
// that matches Astronomer's scope: adopting existing clusters and applying
// optional baselines, not provisioning infrastructure.

import { useParams } from '@/lib/navigation';
import { Activity } from 'lucide-react';
import { RegistrationTimeline } from '@/components/clusters/registration-timeline';

export default function ClusterAdoptionPage() {
  const params = useParams();
  const clusterId = String(params?.id ?? '');

  return (
    <div className="p-4 space-y-4">
      <div>
        <h1 className="text-xl font-semibold flex items-center gap-2">
          <Activity className="h-5 w-5" />
          Adoption
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Step-by-step record of how this existing cluster was registered, connected, and brought
          under optional baseline management. Updates live as new steps land.
        </p>
      </div>
      <RegistrationTimeline clusterId={clusterId} embedded />
    </div>
  );
}
