import { useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { toastError } from "@/lib/toast";
import { Server, Info, AlertTriangle } from "lucide-react";
import { createCluster, updateCluster } from "@/lib/api/clusters";
import { setRegistrationOptions } from "@/lib/api/cluster-registration";
import { useCluster } from "@/lib/hooks/clusters";
import { useClusterSearch } from "@/lib/hooks/cluster-search";
import { useAppForm, useStore } from "@/lib/form";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { FormShell } from "@/components/ui/form-shell";
import { ActionButton } from "@/components/ui/action-button";
import { QueryStates } from "@/components/ui/query-states";
import { RegistrationConnectStep } from "@/components/clusters/registration-connect-step";
import {
  parseRegistrationSearch,
  registrationSearch,
} from "@/components/clusters/registration-flow";
import {
  REGISTRATION_STEPS,
  WizardStepper,
} from "@/components/ui/wizard-stepper";
import type { Cluster, ClusterEnvironment } from "@/types";

export function RegisterClusterWizardRoute() {
  const navigate = useNavigate();
  const { clusterId } = Route.useSearch();
  const draftClusterId = clusterId ?? null;
  // `?clusterId=` alone means "there is a draft"; it stays in the URL for
  // both step 2 (just arrived) and step 1 (backed out) so the draft's
  // identity — and therefore the name-uniqueness self-exclusion below —
  // survives a refresh. Which of those two steps to show is a transient UI
  // choice, not part of the draft's identity, so it lives in a plain
  // boolean rather than reintroducing a string id in local state.
  const [returnedToForm, setReturnedToForm] = useState(false);
  const showForm = draftClusterId !== null && returnedToForm;
  // Always called (Rules of Hooks) — `useCluster` no-ops when the id is
  // empty, and only the `showForm` branch below actually uses the result.
  const clusterQuery = useCluster(draftClusterId ?? "");

  if (draftClusterId && !showForm) {
    return (
      <RegistrationConnectStep
        clusterId={draftClusterId}
        onBack={() => {
          setReturnedToForm(true);
          void navigate({
            to: "/dashboard/clusters/register",
            search: registrationSearch(draftClusterId),
            replace: true,
          });
        }}
      />
    );
  }

  if (draftClusterId) {
    return (
      <QueryStates query={clusterQuery} loadingTitle="Loading draft cluster…">
        {(cluster) => (
          <RegisterClusterWizardPage
            draftClusterId={draftClusterId}
            initialCluster={cluster}
            onRegistered={(id) => {
              setReturnedToForm(false);
              void navigate({
                to: "/dashboard/clusters/register",
                search: registrationSearch(id),
                replace: true,
              });
            }}
          />
        )}
      </QueryStates>
    );
  }

  return (
    <RegisterClusterWizardPage
      draftClusterId={null}
      initialCluster={null}
      onRegistered={(id) => {
        void navigate({
          to: "/dashboard/clusters/register",
          search: registrationSearch(id),
          replace: true,
        });
      }}
    />
  );
}

function RegisterClusterWizardPage({
  draftClusterId,
  initialCluster,
  onRegistered,
}: {
  draftClusterId: string | null;
  initialCluster: Cluster | null;
  onRegistered: (clusterId: string) => void;
}) {
  const navigate = useNavigate();
  const [submissionError, setSubmissionError] = useState<string | null>(null);
  const form = useAppForm({
    defaultValues: {
      name: initialCluster?.name ?? "",
      displayName: initialCluster?.displayName ?? "",
      description: initialCluster?.description ?? "",
      environment:
        (initialCluster?.environment as ClusterEnvironment | undefined) ??
        ("development" as ClusterEnvironment),
      region: initialCluster?.region ?? "",
      installBaseline: initialCluster?.installBaseline ?? false,
      privilegeProfile:
        initialCluster?.agentPrivilegeProfile === "admin" ? "admin" : "viewer",
      apiServerUrl: initialCluster?.apiServerUrl ?? "",
      caCertificate: initialCluster?.caCertificate ?? "",
      agentRequestCPU:
        initialCluster?.agentOverrides?.resources?.requests?.cpu ?? "",
      agentRequestMemory:
        initialCluster?.agentOverrides?.resources?.requests?.memory ?? "",
      agentLimitCPU:
        initialCluster?.agentOverrides?.resources?.limits?.cpu ?? "",
      agentLimitMemory:
        initialCluster?.agentOverrides?.resources?.limits?.memory ?? "",
      agentHTTPSProxy: initialCluster?.agentOverrides?.proxy?.https_proxy ?? "",
      agentHTTPProxy: initialCluster?.agentOverrides?.proxy?.http_proxy ?? "",
      agentNoProxy: initialCluster?.agentOverrides?.proxy?.no_proxy ?? "",
    },
    onSubmit: async ({ value }) => {
      setSubmissionError(null);
      // Old guard (`if (!form.name || nameTaken) return`) — the submit button's
      // disabled gate below is the same condition; re-checked here 1:1.
      if (!value.name || nameTaken) return;
      try {
        const annotations = {
          "astronomer.io/agent-privilege-profile": value.privilegeProfile,
        };
        const agentOverrides = {
          resources: {
            requests: {
              cpu: value.agentRequestCPU || undefined,
              memory: value.agentRequestMemory || undefined,
            },
            limits: {
              cpu: value.agentLimitCPU || undefined,
              memory: value.agentLimitMemory || undefined,
            },
          },
          proxy: {
            https_proxy: value.agentHTTPSProxy || undefined,
            http_proxy: value.agentHTTPProxy || undefined,
            no_proxy: value.agentNoProxy || undefined,
          },
        };
        const cluster = draftClusterId
          ? await updateCluster(draftClusterId, {
              display_name: value.displayName || value.name,
              description: value.description || undefined,
              environment: value.environment,
              region: value.region || undefined,
              annotations,
              api_server_url: value.apiServerUrl || undefined,
              ca_certificate: value.caCertificate || undefined,
              agent_overrides: agentOverrides,
            })
          : await createCluster({
              name: value.name,
              displayName: value.displayName || value.name,
              description: value.description || undefined,
              environment: value.environment,
              // Distribution is detected from node labels/provider IDs after
              // the agent connects; it is never guessed during adoption.
              region: value.region || undefined,
              annotations,
              apiServerUrl: value.apiServerUrl || undefined,
              caCertificate: value.caCertificate || undefined,
              agentOverrides,
            });
        // Record the operator's choice. The backend keeps install_baseline
        // NULL until this call so it can distinguish "hasn't decided" from
        // "opted out". A viewer agent is read-only and physically can't deploy
        // the baseline, so force it off — the UI disables the checkbox under
        // viewer, this guards the submit value too.
        const installBaseline =
          value.privilegeProfile === "viewer" ? false : value.installBaseline;
        await setRegistrationOptions(cluster.id, installBaseline);
        onRegistered(cluster.id);
      } catch (err) {
        setSubmissionError(
          err instanceof Error ? err.message : "The request failed. Try again.",
        );
        const msg = err instanceof Error ? err.message : "Unknown error";
        toastError(`Failed to register cluster: ${msg}`);
      }
    },
  });

  const name = useStore(form.store, (s) => s.values.name);
  // Name availability is a bounded server search; the create endpoint remains
  // the final uniqueness authority if another registration races this check.
  const [debouncedName] = useDebouncedValue(name.trim(), { wait: 250 });
  const nameMatches = useClusterSearch(debouncedName, debouncedName.length > 0);
  const submitting = useStore(form.store, (s) => s.isSubmitting);
  const privilegeProfile = useStore(
    form.store,
    (s) => s.values.privilegeProfile,
  );
  const nameTaken =
    name.length > 0 &&
    (nameMatches.data?.pages ?? []).some((page) =>
      page.data.some(
        (cluster) =>
          cluster.id !== draftClusterId &&
          cluster.name.toLowerCase() === name.trim().toLowerCase(),
      ),
    );
  const isViewer = privilegeProfile === "viewer";

  return (
    <div>
      <div className="mb-6">
        <div className="flex items-center gap-3 mb-2">
          <div className="w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
            <Server className="h-5 w-5 text-muted-foreground" />
          </div>
          <div>
            <h1 className="text-2xl font-semibold text-foreground">
              Register an existing cluster
            </h1>
          </div>
        </div>
        <p className="text-sm text-muted-foreground">
          Connect a Kubernetes cluster you already run so Astronomer can observe
          and manage it. Astronomer does not create clusters, provision
          infrastructure, or add nodes — you install a lightweight agent and it
          adopts the cluster as-is.
        </p>
        <WizardStepper
          steps={REGISTRATION_STEPS}
          currentStep={1}
          className="mt-5"
        />
      </div>

      <FormShell
        onSubmit={(e) => {
          e.preventDefault();
          void form.handleSubmit();
        }}
        className="space-y-5"
      >
        <form.AppForm>
          <form.FormErrorSummary serverError={submissionError} />
        </form.AppForm>
        <Field label="Cluster name" required>
          <form.Field name="name">
            {(field) => (
              <Input
                name={field.name}
                type="text"
                value={field.state.value}
                onChange={(e) =>
                  field.handleChange(
                    e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, "-"),
                  )
                }
                onBlur={field.handleBlur}
                placeholder="my-cluster"
                className={nameTaken ? "border-status-error" : undefined}
                data-initial-focus
              />
            )}
          </form.Field>
          {nameTaken && (
            <p className="mt-1 flex items-center gap-1.5 text-xs text-status-error">
              <AlertTriangle className="h-3.5 w-3.5 shrink-0" />A cluster named
              &quot;{name}&quot; already exists. Choose a different name.
            </p>
          )}
        </Field>

        <Field label="Display name">
          <form.Field name="displayName">
            {(field) => (
              <Input
                name={field.name}
                type="text"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="My Production Cluster"
              />
            )}
          </form.Field>
        </Field>

        <Field label="Description">
          <form.Field name="description">
            {(field) => (
              <Textarea
                name={field.name}
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="Brief description..."
                rows={2}
                className="min-h-0 resize-none text-sm font-sans"
              />
            )}
          </form.Field>
        </Field>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <Field label="Environment">
            <form.Field name="environment">
              {(field) => (
                <Select
                  name={field.name}
                  aria-label="Environment"
                  value={field.state.value}
                  onChange={(e) =>
                    field.handleChange(e.target.value as ClusterEnvironment)
                  }
                  onBlur={field.handleBlur}
                >
                  <option value="development">Development</option>
                  <option value="staging">Staging</option>
                  <option value="production">Production</option>
                  <option value="testing">Testing</option>
                </Select>
              )}
            </form.Field>
          </Field>
          <Field label="Region">
            <form.Field name="region">
              {(field) => (
                <Input
                  name={field.name}
                  type="text"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                  placeholder="us-east-1"
                />
              )}
            </form.Field>
          </Field>
        </div>

        <p className="text-xs text-muted-foreground -mt-2">
          Kubernetes distribution (k3s, RKE2, EKS, AKS, GKE, OpenShift…) is
          detected automatically from the cluster once the agent connects.
        </p>

        <Field label="Direct Kubernetes API endpoint (optional)">
          <form.Field name="apiServerUrl">
            {(field) => (
              <Input
                name={field.name}
                type="url"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="https://api.example.com:6443"
              />
            )}
          </form.Field>
          <p className="mt-1 text-xs text-muted-foreground">
            Enables a separate 15-minute, read-only direct kubeconfig after the
            endpoint and TLS identity are verified. In-cluster .svc addresses
            and proxy credentials are never used.
          </p>
        </Field>

        <Field label="Direct API CA certificate (optional)">
          <form.Field name="caCertificate">
            {(field) => (
              <Textarea
                name={field.name}
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
                placeholder="-----BEGIN CERTIFICATE-----"
                rows={4}
                className="font-mono text-xs"
              />
            )}
          </form.Field>
        </Field>

        <section className="space-y-4 rounded-lg border border-border bg-muted/20 p-4">
          <div>
            <h2 className="text-sm font-medium text-foreground">
              Agent runtime
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              Optional Kubernetes resource quantities and egress proxy settings.
              Defaults are applied when fields are empty.
            </p>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {(
              [
                ["agentRequestCPU", "Request CPU", "100m"],
                ["agentRequestMemory", "Request memory", "128Mi"],
                ["agentLimitCPU", "Limit CPU", "500m"],
                ["agentLimitMemory", "Limit memory", "512Mi"],
              ] as const
            ).map(([name, label, placeholder]) => (
              <Field key={name} label={label}>
                <form.Field name={name}>
                  {(field) => (
                    <Input
                      name={field.name}
                      value={field.state.value}
                      onChange={(event) =>
                        field.handleChange(event.target.value)
                      }
                      onBlur={field.handleBlur}
                      placeholder={placeholder}
                    />
                  )}
                </form.Field>
              </Field>
            ))}
          </div>
          <div className="space-y-3">
            {(
              [
                [
                  "agentHTTPSProxy",
                  "HTTPS proxy",
                  "http://proxy.internal:3128",
                ],
                ["agentHTTPProxy", "HTTP proxy", "http://proxy.internal:3128"],
                ["agentNoProxy", "NO_PROXY", ".svc,.cluster.local,10.0.0.0/8"],
              ] as const
            ).map(([name, label, placeholder]) => (
              <Field key={name} label={`${label} (optional)`}>
                <form.Field name={name}>
                  {(field) => (
                    <Input
                      name={field.name}
                      value={field.state.value}
                      onChange={(event) =>
                        field.handleChange(event.target.value)
                      }
                      onBlur={field.handleBlur}
                      placeholder={placeholder}
                    />
                  )}
                </form.Field>
              </Field>
            ))}
          </div>
        </section>

        <Field label="Agent privilege profile">
          <form.Field name="privilegeProfile">
            {(field) => (
              <Select
                name={field.name}
                aria-label="Agent privilege profile"
                value={field.state.value}
                onChange={(e) => field.handleChange(e.target.value)}
                onBlur={field.handleBlur}
              >
                <option value="viewer">
                  Viewer — Astronomer observes (read-only)
                </option>
                <option value="admin">
                  Admin — Astronomer operates (governed by user RBAC)
                </option>
              </Select>
            )}
          </form.Field>
          <p className="mt-1 text-xs text-muted-foreground">
            Sets the ceiling for what Astronomer can do on this cluster.{" "}
            <span className="font-medium text-foreground">Viewer</span> is
            read-only — Astronomer can observe the cluster, and no user can
            change it regardless of their Astronomer role (safe first adoption,
            trivially removable).{" "}
            <span className="font-medium text-foreground">Admin</span> lets
            Astronomer operate the cluster; what each user can actually do is
            then governed by their Astronomer RBAC. (Finer-grained operator /
            namespace-scoped profiles are available via the API.)
          </p>
          {isViewer ? (
            <p className="mt-2 flex items-start gap-1.5 text-xs text-status-warning">
              <AlertTriangle className="h-3.5 w-3.5 shrink-0 mt-px" />
              <span>
                Read-only: Astronomer{" "}
                <span className="font-medium">
                  cannot install baseline monitoring
                </span>{" "}
                (kube-state-metrics, node-exporter, etc.) on a viewer cluster.
                You&apos;ll get inventory, logs, and health — but no metrics
                dashboards until you re-adopt as Admin.
              </span>
            </p>
          ) : (
            <p className="mt-2 flex items-start gap-1.5 text-xs text-status-success">
              <Info className="h-3.5 w-3.5 shrink-0 mt-px" />
              <span>
                Astronomer can install and manage baseline monitoring and tools
                on this cluster.
              </span>
            </p>
          )}
        </Field>

        <label
          className={`flex items-start gap-3 p-4 rounded-lg border border-border transition-colors ${
            isViewer
              ? "bg-muted/10 opacity-60 cursor-not-allowed"
              : "bg-muted/20 cursor-pointer hover:bg-muted/30"
          }`}
        >
          <form.Field name="installBaseline">
            {(field) => (
              <Input
                name={field.name}
                type="checkbox"
                // A viewer agent can't deploy — force unchecked and disabled so the
                // read-only + install-baseline contradiction can't be submitted.
                checked={isViewer ? false : field.state.value}
                disabled={isViewer}
                onChange={(e) => field.handleChange(e.target.checked)}
                onBlur={field.handleBlur}
                className="mt-0.5 h-4 w-4 rounded-sm border-border text-primary focus:ring-ring disabled:cursor-not-allowed"
              />
            )}
          </form.Field>
          <div className="flex-1">
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium text-foreground">
                Quick Start: Install Platform Baseline after cluster connects
              </span>
              <Info
                className="h-3.5 w-3.5 text-muted-foreground"
                aria-label="Installs trivy-operator, kube-state-metrics, prometheus-node-exporter, fluent-bit, ingress-nginx, cert-manager, gatekeeper"
              />
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              Installs platform baseline components after the agent connects:
              trivy-operator, kube-state-metrics, prometheus-node-exporter,
              fluent-bit, ingress-nginx, cert-manager, and Gatekeeper. Leave
              unchecked for a bare cluster — you can install these later from
              the Cluster Tools tab.
            </p>
            {isViewer && (
              <p className="mt-1.5 text-xs font-medium text-status-warning">
                Unavailable under Viewer — a read-only agent can&apos;t deploy.
                Choose Admin above to enable.
              </p>
            )}
          </div>
        </label>

        <div className="flex items-center justify-end gap-2 pt-2">
          <ActionButton
            type="button"
            onClick={() => void navigate({ to: "/dashboard/clusters" })}
          >
            Cancel
          </ActionButton>
          <ActionButton
            type="submit"
            intent="primary"
            disabled={!name || nameTaken || submitting}
            loading={submitting}
            loadingLabel="Next: Get install command →"
          >
            Next: Get install command →
          </ActionButton>
        </div>
      </FormShell>
    </div>
  );
}

function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        {label}
        {required && <span className="text-status-error ml-1">*</span>}
      </label>
      {children}
    </div>
  );
}

export const Route = createFileRoute("/dashboard/clusters/register/")({
  validateSearch: parseRegistrationSearch,
  component: RegisterClusterWizardRoute,
});
