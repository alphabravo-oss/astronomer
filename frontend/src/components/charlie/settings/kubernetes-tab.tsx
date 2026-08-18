import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, Loader2, Shield } from "lucide-react";
import {
  acknowledgeCharlieDisclosure,
  getCharlieKubernetesVisibility,
  updateCharlieKubernetesVisibility,
  type CharlieKubernetesVisibilityProfile,
} from "@/lib/api/charlie-admin";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { cn } from "@/lib/utils";
import { Meta, Section, Unavailable, button, primary } from "./shared";

const profileLabels: Record<CharlieKubernetesVisibilityProfile, string> = {
  disabled: "Disabled",
  product_namespace: "Product namespace",
  cluster_diagnostics: "Cluster diagnostics",
};

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
    mutationFn: async () => {
      const latest = await getCharlieKubernetesVisibility();
      return updateCharlieKubernetesVisibility({
        profile,
        podLogs: profile !== "disabled" && podLogs,
        revision: latest.revision,
      });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminKubernetesVisibility });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      toastSuccess("Kubernetes visibility updated and catalog rediscovered");
    },
    onError: (error) => toastApiError("Kubernetes visibility update failed", error),
  });
  const acceptCatalog = useMutation({
    mutationFn: (digest: string) => acknowledgeCharlieDisclosure(digest),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminKubernetesVisibility });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminMode });
      toastSuccess("Rediscovered catalog accepted");
    },
    onError: (error) => toastApiError("Catalog acceptance failed", error),
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
        <div className="space-y-2">
          <span className="text-sm font-medium">Visibility profile</span>
          <div
            role="radiogroup"
            aria-label="Kubernetes visibility profile"
            className="grid gap-2"
          >
            {current.availableProfiles.map((value) => {
              const selected = profile === value;
              return (
                <button
                  key={value}
                  type="button"
                  role="radio"
                  aria-label={profileLabels[value]}
                  aria-checked={selected}
                  disabled={!configured}
                  onClick={() => setProfile(value)}
                  className={cn(
                    "rounded-lg border p-3 text-left transition-colors motion-reduce:transition-none",
                    selected
                      ? "border-primary bg-primary/10 text-foreground"
                      : "border-border bg-background text-foreground hover:bg-accent",
                    !configured && "cursor-not-allowed opacity-50",
                  )}
                >
                  <span className="block text-sm font-medium">{profileLabels[value]}</span>
                  <span className="mt-1 block text-xs text-muted-foreground">{descriptions[value]}</span>
                </button>
              );
            })}
          </div>
        </div>
        <div className="flex items-start gap-3 rounded-lg border border-border bg-background p-3 text-sm">
          <input
            id="charlie-kubernetes-pod-logs"
            type="checkbox"
            checked={profile !== "disabled" && podLogs}
            disabled={!configured || profile === "disabled"}
            onChange={(event) => setPodLogs(event.target.checked)}
            className="mt-0.5 h-4 w-4 shrink-0 rounded border-border bg-background accent-primary"
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
          <div className="space-y-2 rounded-lg border border-status-warning/30 bg-status-warning/5 p-3 text-xs">
            <p>Rediscovery completed. Accept this catalog on Astronomer before raising mode. Charlie does not accept product capabilities.</p>
            {current.candidateDisclosureDigest && (
              <p className="break-all text-muted-foreground">Digest: {current.candidateDisclosureDigest}</p>
            )}
            <button
              type="button"
              className={button}
              disabled={!current.candidateDisclosureDigest || acceptCatalog.isPending}
              onClick={() => current.candidateDisclosureDigest && acceptCatalog.mutate(current.candidateDisclosureDigest)}
            >
              Accept rediscovered catalog
            </button>
          </div>
        )}
        {current.requiresProductAcknowledgement && (
          <p className="rounded-lg border border-status-warning/30 bg-status-warning/5 p-3 text-xs">Accept the published disclosure on the Mode tab before restoring authority.</p>
        )}
      </Section>
    </div>
  );
}
