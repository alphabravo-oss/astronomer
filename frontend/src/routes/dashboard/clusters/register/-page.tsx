import { useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import { getRouteApi, useNavigate } from "@tanstack/react-router";
import { toastError } from "@/lib/toast";
import { Server, AlertTriangle } from "lucide-react";
import {
  RegistrationBaselineOption,
  RegistrationImageScanningOption,
} from "@/components/clusters/registration-baseline-options";
import { createCluster, updateCluster } from "@/lib/api/clusters";
import { setRegistrationOptions } from "@/lib/api/cluster-registration";
import { useCluster } from "@/lib/hooks/clusters";
import { useClusterSearch } from "@/lib/hooks/cluster-search";
import { useAppForm, useStore } from "@/lib/form";
import { errorMessages } from "@/components/form/error-summary";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { FormShell } from "@/components/ui/form-shell";
import { ActionButton } from "@/components/ui/action-button";
import { PageHeader } from "@/components/ui/page";
import { Field } from "@/components/form/fields";
import { QueryStates } from "@/components/ui/query-states";
import { RegistrationConnectStep } from "@/components/clusters/registration-connect-step";
import { registrationSearch } from "@/components/clusters/registration-flow";
import {
  REGISTRATION_STEPS,
  WizardStepper,
} from "@/components/ui/wizard-stepper";
import type { Cluster, ClusterEnvironment } from "@/types";

// Mirrors the backend's RFC-1123-slug `validate:"required,rfc1123"` tag
// (internal/handler/clusters_inventory.go) restricted to the documented
// convention (OpenAPI: "RFC-1123 slug; must be lowercase + hyphens").
const CLUSTER_NAME_PATTERN = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

const routeApi = getRouteApi("/dashboard/clusters/register/");

function registrationDefaults(initialCluster: Cluster | null) {
  return {
    name: initialCluster?.name ?? "",
    displayName: initialCluster?.displayName ?? "",
    description: initialCluster?.description ?? "",
    environment:
      (initialCluster?.environment as ClusterEnvironment | undefined) ??
      ("development" as ClusterEnvironment),
    region: initialCluster?.region ?? "",
    installBaseline: initialCluster?.installBaseline ?? true,
    installImageScanning:
      initialCluster?.annotations?.["astronomer.io/image-scanning"] !==
      "disabled",
    apiServerUrl: initialCluster?.apiServerUrl ?? "",
    caCertificate: initialCluster?.caCertificate ?? "",
    agentRequestCPU:
      initialCluster?.agentOverrides?.resources?.requests?.cpu ?? "",
    agentRequestMemory:
      initialCluster?.agentOverrides?.resources?.requests?.memory ?? "",
    agentLimitCPU: initialCluster?.agentOverrides?.resources?.limits?.cpu ?? "",
    agentLimitMemory:
      initialCluster?.agentOverrides?.resources?.limits?.memory ?? "",
    agentHTTPSProxy: initialCluster?.agentOverrides?.proxy?.https_proxy ?? "",
    agentHTTPProxy: initialCluster?.agentOverrides?.proxy?.http_proxy ?? "",
    agentNoProxy: initialCluster?.agentOverrides?.proxy?.no_proxy ?? "",
  };
}

export function RegisterClusterWizardRoute() {
  const navigate = useNavigate();
  const { clusterId } = routeApi.useSearch();
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
    defaultValues: registrationDefaults(initialCluster),
    onSubmit: async ({ value }) => {
      setSubmissionError(null);
      // Old guard (`if (!form.name || nameTaken) return`) — the submit button's
      // disabled gate below is the same condition; re-checked here 1:1.
      if (!value.name || nameTaken) return;
      try {
        const annotations = {
          ...initialCluster?.annotations,
          "astronomer.io/agent-privilege-profile": "admin",
          "astronomer.io/image-scanning": value.installImageScanning
            ? "enabled"
            : "disabled",
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
        await setRegistrationOptions(cluster.id, value.installBaseline);
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
  const nameFieldError = useStore(
    form.store,
    (s) => errorMessages(s.fieldMeta.name?.errors ?? [])[0],
  );
  // Name availability is a bounded server search; the create endpoint remains
  // the final uniqueness authority if another registration races this check.
  const [debouncedName] = useDebouncedValue(name.trim(), { wait: 250 });
  const nameMatches = useClusterSearch(debouncedName, debouncedName.length > 0);
  const submitting = useStore(form.store, (s) => s.isSubmitting);
  const nameTaken =
    name.length > 0 &&
    (nameMatches.data?.pages ?? []).some((page) =>
      page.data.some(
        (cluster) =>
          cluster.id !== draftClusterId &&
          cluster.name.toLowerCase() === name.trim().toLowerCase(),
      ),
    );

  return (
    <div>
      <div className="mb-6">
        <PageHeader
          title={
            <span className="inline-flex items-center gap-3">
              <span className="w-10 h-10 rounded-lg bg-muted flex items-center justify-center">
                <Server className="h-5 w-5 text-muted-foreground" />
              </span>
              Register an existing cluster
            </span>
          }
          description="Connect a Kubernetes cluster you already run so Astronomer can observe and manage it. Astronomer does not create clusters, provision infrastructure, or add nodes — you install a lightweight agent and it adopts the cluster as-is."
        />
        <WizardStepper
          steps={REGISTRATION_STEPS}
          currentStep={1}
          className="mt-5"
        />
      </div>

      <FormShell
        form={form}
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
          <form.Field
            name="name"
            validators={{
              onChange: ({ value }) =>
                !CLUSTER_NAME_PATTERN.test(value)
                  ? "Name must be a lowercase RFC-1123 slug (letters, digits, hyphens; can't start or end with a hyphen)"
                  : undefined,
            }}
          >
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
          {!nameTaken && nameFieldError && (
            <p className="mt-1 text-xs text-status-error">{nameFieldError}</p>
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

        <p className="text-sm text-muted-foreground">
          Astronomer manages this cluster. Each user's RBAC permissions control
          which resources they can access and which actions they can perform.
        </p>

        <form.Field name="installBaseline">
          {(field) => (
            <RegistrationBaselineOption
              name={field.name}
              checked={field.state.value}
              disabled={false}
              onChange={field.handleChange}
              onBlur={field.handleBlur}
            />
          )}
        </form.Field>
        <form.Field name="installImageScanning">
          {(field) => (
            <RegistrationImageScanningOption
              name={field.name}
              checked={field.state.value}
              disabled={false}
              onChange={field.handleChange}
              onBlur={field.handleBlur}
            />
          )}
        </form.Field>

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
