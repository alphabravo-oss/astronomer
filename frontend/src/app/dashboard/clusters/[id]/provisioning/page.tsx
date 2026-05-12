'use client';

// Sprint 23 — Provisioning tab on cluster detail.
//
// Mirrors the wizard's progress page but lives inside the cluster
// detail nav so an operator returning to a half-provisioned cluster
// (or one that's been ready for days) can see the same step timeline.
// Uses the shared RegistrationTimeline component so both surfaces stay
// in lockstep.

import { useParams } from 'next/navigation';
import { Activity } from 'lucide-react';
import { RegistrationTimeline } from '@/components/clusters/registration-timeline';

export default function ClusterProvisioningPage() {
  const params = useParams();
  const clusterId = String(params?.id ?? '');

  return (
    <div className="p-4 space-y-4">
      <div>
        <h1 className="text-xl font-semibold flex items-center gap-2">
          <Activity className="h-5 w-5" />
          Provisioning
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Step-by-step record of how this cluster came online, baseline operator installs, and any
          drift sweeps. Updates live as new steps land.
        </p>
      </div>
      <RegistrationTimeline clusterId={clusterId} embedded />
    </div>
  );
}
