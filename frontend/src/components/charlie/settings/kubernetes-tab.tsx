import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, Loader2, Shield } from "lucide-react";
import {
  getCharlieKubernetesVisibility,
  updateCharlieKubernetesVisibility,
  type CharlieKubernetesVisibilityProfile,
} from "@/lib/api/charlie-admin";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { Meta, Section, Unavailable, primary } from "./shared";

const descriptions: Record<CharlieKubernetesVisibilityProfile, string> = {
  disabled: "No Kubernetes API capabilities are disclosed to Charlie.",
  product_namespace: "Product-owned workloads, pods, events, networking, storage, jobs, availability, and metrics in the Astronomer namespace.",
  cluster_diagnostics: "Product namespace visibility plus bounded node and cluster-health metadata.",
};

export function KubernetesTab() {
  const qc = useQueryClient();
  const query = useQuery({
    queryKey: queryKeys.charlie.adminKubernetesVisibility,
    queryFn: getCharlieKubernetesVisibility,
  });
  const [profile, setProfile] = useState<CharlieKubernetesVisibilityProfile>("disabled");
  const [podLogs, setPodLogs] = useState(false);
  useEffect(() => {
    if (!query.data) return;
    setProfile(query.data.profile);
    setPodLogs(query.data.podLogs);
  }, [query.data]);
  const update = useMutation({
    mutationFn: () => updateCharlieKubernetesVisibility({
      profile,
      podLogs: profile !== "disabled" && podLogs,
      revision: query.data?.revision ?? -1,
    }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminKubernetesVisibility });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess("Kubernetes visibility updated and catalog rediscovered");
    },
    onError: (error) => toastApiError("Kubernetes visibility update failed", error),
  });
  if (query.isLoading) return <Loader2 className="h-5 w-5 animate-spin motion-reduce:animate-none" />;
  if (query.isError || !query.data) return <Unavailable name="Kubernetes visibility" retry={() => void query.refetch()} />;
  const current = query.data;
  const configured = current.state !== "not_configured";
  const dirty = current.requiresRediscovery || profile !== current.profile || (profile !== "disabled" && podLogs !== current.podLogs);
  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Section
        title="Kubernetes API visibility"
        description="Choose what the Astronomer-owned MCP adapter may observe. This is independent from Charlie's read-only, approval, or automatic action mode."
      >
        <label className="space-y-1 text-sm">
          <span className="font-medium">Visibility profile</span>
          <select
            aria-label="Kubernetes visibility profile"
            className="field"
            value={profile}
            disabled={!configured}
            onChange={(event) => setProfile(event.target.value as CharlieKubernetesVisibilityProfile)}
          >
            {current.availableProfiles.map((value) => (
              <option key={value} value={value}>{value.replaceAll("_", " ")}</option>
            ))}
          </select>
        </label>
        <p className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">{descriptions[profile]}</p>
        <div className="flex items-start gap-3 rounded-lg border p-3 text-sm">
          <input
            id="charlie-kubernetes-pod-logs"
            type="checkbox"
            checked={profile !== "disabled" && podLogs}
            disabled={!configured || profile === "disabled"}
            onChange={(event) => setPodLogs(event.target.checked)}
            className="mt-0.5"
          />
          <label htmlFor="charlie-kubernetes-pod-logs">
            <strong className="block">Bounded, redacted pod log tails</strong>
            <span className="text-xs text-muted-foreground">Content access is separately disclosed and can be disabled while retaining resource status.</span>
          </label>
        </div>
        <div className="rounded-lg border border-status-warning/30 bg-status-warning/5 p-3 text-xs">
          <div className="flex items-center gap-2 font-medium"><AlertTriangle className="h-4 w-4" /> Authority reset on scope change</div>
          <p className="mt-1 text-muted-foreground">Saving closes admission, sets requested and verified authority to disabled, and asks the local Charlie agent to rediscover the exact new catalog. Central and product acknowledgement remain explicit.</p>
        </div>
        {!configured && (
          <p className="rounded-lg border border-status-warning/30 bg-status-warning/5 p-3 text-xs">Connect and enable the Charlie product agent before selecting a Kubernetes visibility profile.</p>
        )}
        <button disabled={!configured || !dirty || update.isPending} className={primary} onClick={() => update.mutate()}>
          {update.isPending ? <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" /> : <Shield className="h-4 w-4" />}
          {current.requiresRediscovery && profile === current.profile && podLogs === current.podLogs ? "Retry catalog rediscovery" : "Save visibility policy"}
        </button>
      </Section>
      <Section title="Effective boundary" description="This status is product-owned. Charlie central receives only content-free connector provenance in the capability digest.">
        <dl className="grid grid-cols-2 gap-4">
          <Meta label="State" value={current.state} />
          <Meta label="Profile" value={current.profile.replaceAll("_", " ")} />
          <Meta label="Runtime" value={current.instanceId} />
          <Meta label="Namespaces" value={current.namespaces.join(", ") || "None"} />
          <Meta label="Cluster-scoped metadata" value={current.clusterScoped ? "Allowed" : "Not allowed"} />
          <Meta label="Pod logs" value={current.podLogs ? "Bounded and redacted" : "Not allowed"} />
        </dl>
        <p className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">{current.scopeSummary}</p>
        <div className="space-y-2 rounded-lg border border-status-success/30 bg-status-success/5 p-3 text-xs">
          <div className="flex items-center gap-2 font-medium"><CheckCircle2 className="h-4 w-4 text-status-success" /> Hard prohibitions in every profile</div>
          <p>Downstream clusters, Secret values, exec, attach, port-forward, arbitrary API proxying, and unrestricted resource selectors are unavailable.</p>
        </div>
        {current.requiresRediscovery && (
          <p className="rounded-lg border border-status-warning/30 bg-status-warning/5 p-3 text-xs">The connector catalog still requires rediscovery. Save the unchanged policy to retry the signed, installation-bound request.</p>
        )}
        {current.requiresCentralReview && (
          <p className="rounded-lg border border-status-warning/30 bg-status-warning/5 p-3 text-xs">Rediscovery completed. Review candidate catalog <code>{current.candidateDisclosureDigest}</code> in Charlie. Charlie publishes the authoritative disclosure after any intentional mode or allowlist changes.</p>
        )}
        {current.requiresProductAcknowledgement && (
          <p className="rounded-lg border border-status-warning/30 bg-status-warning/5 p-3 text-xs">Charlie accepted the new catalog. Acknowledge Charlie's authoritative disclosure in Astronomer's Mode tab before restoring authority.</p>
        )}
      </Section>
    </div>
  );
}
