// Package kubectl owns the in-browser shell sprint-17 feature: spin up
// an ephemeral debug pod in a managed cluster, bind it to a short-lived
// ServiceAccount whose RBAC mirrors the operator's effective verbs, and
// proxy stdin/stdout via the sprint-14 tunnel.ExecConsumer.
//
// This file owns the manifest builders. The Open/Close/Reap lifecycle
// lives in session.go; the WS streaming bridge lives in stream.go.
//
// All names that flow into the cluster are of the form
// `astro-shell-<base32(uuid)>` — no caller-controlled string ever
// reaches the k8s API server, so the manifests are immune to injection.
package kubectl

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// DefaultImage is the kubectl debug pod image. We ship our own
// (deploy/docker/Dockerfile.shell — alpine + kubectl pulled directly
// from dl.k8s.io, the Kubernetes Project's release CDN) so the shell
// supply chain is fully under Astronomer's control. Previous
// third-party choices each had problems:
//
//   - bitnami/kubectl:1.31 — 404'd on docker.io during the 2026
//     Bitnami retag, breaking every fresh-cluster shell open.
//   - rancher/kubectl — distroless, so /bin/sh missing → kubectl exec
//     can't attach an interactive terminal.
//   - alpine/k8s — works but tied to a release schedule we don't
//     control; we can't promise an operator that a tag they pinned
//     six months ago will still resolve.
//
// Operators with a private mirror still override via chart value
// kubectlShell.image (config knob `kubectl_shell_image`).
const DefaultImage = "astronomer-shell:dev"

// DefaultNamespace is where every shell pod + SA lives. Kept in
// kube-system because that's the conventional break-glass namespace
// and is unlikely to clash with operator workloads.
const DefaultNamespace = "kube-system"

// containerName must match the EXEC_START container field. The frontend
// passes this verbatim to the WS endpoint.
const ContainerName = "shell"

// shortIDLen is the number of base32 chars we keep from the UUID. 13
// chars × 5 bits = 65 bits of entropy — plenty for a 4-hour-cap session
// while keeping the resulting k8s object name well under 63 chars
// (DNS-1123 label cap on names like ServiceAccount + Pod).
const shortIDLen = 13

// ShortID returns a DNS-1123-safe random suffix. UUID v4 gives us 122
// bits of entropy; base32 (without padding, lowercase) plays nicely
// inside k8s names. Truncated to shortIDLen to keep "astro-shell-<x>"
// well under the 63-char label cap.
func ShortID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand should never fail; fall back to uuid.New() which
		// internally uses the same source but with a wrapped error path
		// that panics on failure.
		u := uuid.New()
		copy(buf[:], u[:])
	}
	enc := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf[:]))
	if len(enc) > shortIDLen {
		enc = enc[:shortIDLen]
	}
	return enc
}

// Names bundles the k8s object names for one session. Generated once at
// Open() time and persisted to the DB row so the reaper can clean up
// even if the in-memory state is lost.
type Names struct {
	SAName       string
	SANamespace  string
	RoleName     string
	BindingName  string
	PodName      string
	PodNamespace string
	// Container is constant ("shell") but stamped on Names so the
	// stream layer doesn't have to import this file's constant.
	Container string
}

// NewNames builds a Names bundle keyed off ShortID().
func NewNames() Names {
	id := ShortID()
	return Names{
		SAName:       "astro-shell-" + id,
		SANamespace:  DefaultNamespace,
		RoleName:     "astro-shell-" + id,
		BindingName:  "astro-shell-" + id,
		PodName:      "astro-shell-" + id,
		PodNamespace: DefaultNamespace,
		Container:    ContainerName,
	}
}

// EffectiveVerbs bounds the requested shell capabilities. WithPermissions
// derives the actual resource rules within that envelope. Non-superusers
// without a derived policy receive no Kubernetes grants.
type EffectiveVerbs struct {
	Read        bool
	Update      bool
	Delete      bool
	ReadSecrets bool
	ExecPods    bool
	Superuser   bool
	policy      *shellPolicy
}

type shellPolicy struct {
	rules []map[string]any
}

// WithPermissions intersects the candidate shell surface with proven caller
// grants. Each resource/verb is checked independently; granting Secret get
// never implicitly grants list/watch. A missing predicate yields no rules.
func (e EffectiveVerbs) WithPermissions(allows func(apiGroup, resource, verb string) bool) EffectiveVerbs {
	rules := make([]map[string]any, 0)
	if allows != nil {
		for _, candidate := range candidateShellRules(e) {
			for _, group := range candidate["apiGroups"].([]string) {
				for _, resource := range candidate["resources"].([]string) {
					verbs := make([]string, 0)
					for _, verb := range candidate["verbs"].([]string) {
						if allows(group, resource, verb) {
							verbs = append(verbs, verb)
						}
					}
					if len(verbs) > 0 {
						rules = append(rules, map[string]any{"apiGroups": []string{group}, "resources": []string{resource}, "verbs": verbs})
					}
				}
			}
		}
	}
	e.policy = &shellPolicy{rules: rules}
	return e
}

func shellRules(e EffectiveVerbs) []map[string]any {
	if e.policy == nil {
		return []map[string]any{}
	}
	return e.policy.rules
}

// candidateShellRules enumerates the Kubernetes resources available to a
// caller-derived non-superuser policy. Kubernetes RBAC has no "all resources except Secrets"
// expression, so a wildcard resource grant can never be made safe. Keep the
// broad diagnostic read surface separate from the smaller mutation surface;
// in particular, mutation access never includes RBAC, ServiceAccounts, CRDs,
// nodes, namespaces, or storage classes.
func candidateShellRules(e EffectiveVerbs) []map[string]any {
	if e.Superuser {
		return nil
	}
	rules := make([]map[string]any, 0, 16)
	add := func(apiGroups, resources, verbs []string) {
		if len(verbs) == 0 {
			return
		}
		rules = append(rules, map[string]any{
			"apiGroups": apiGroups,
			"resources": resources,
			"verbs":     verbs,
		})
	}
	readVerbs := []string(nil)
	if e.Read || e.Update || e.Delete {
		readVerbs = []string{"get", "list", "watch"}
	}
	writeVerbs := []string(nil)
	if e.Update || e.Delete {
		writeVerbs = []string{"create", "update", "patch"}
	}
	if e.Delete {
		writeVerbs = append(writeVerbs, "delete")
	}

	add([]string{""}, []string{"configmaps", "endpoints", "events", "limitranges", "namespaces", "nodes", "persistentvolumeclaims", "persistentvolumes", "pods", "pods/log", "pods/status", "replicationcontrollers", "resourcequotas", "serviceaccounts", "services"}, readVerbs)
	add([]string{"apps"}, []string{"controllerrevisions", "daemonsets", "deployments", "replicasets", "statefulsets"}, readVerbs)
	add([]string{"autoscaling"}, []string{"horizontalpodautoscalers"}, readVerbs)
	add([]string{"batch"}, []string{"cronjobs", "jobs"}, readVerbs)
	add([]string{"coordination.k8s.io"}, []string{"leases"}, readVerbs)
	add([]string{"discovery.k8s.io"}, []string{"endpointslices"}, readVerbs)
	add([]string{"networking.k8s.io"}, []string{"ingressclasses", "ingresses", "networkpolicies"}, readVerbs)
	add([]string{"policy"}, []string{"poddisruptionbudgets"}, readVerbs)
	add([]string{"rbac.authorization.k8s.io"}, []string{"clusterrolebindings", "clusterroles", "rolebindings", "roles"}, readVerbs)
	add([]string{"storage.k8s.io"}, []string{"csidrivers", "csinodes", "csistoragecapacities", "storageclasses", "volumeattachments"}, readVerbs)
	add([]string{"apiextensions.k8s.io"}, []string{"customresourcedefinitions"}, readVerbs)

	add([]string{""}, []string{"configmaps", "persistentvolumeclaims", "pods", "replicationcontrollers", "services"}, writeVerbs)
	add([]string{"apps"}, []string{"daemonsets", "deployments", "replicasets", "statefulsets"}, writeVerbs)
	add([]string{"autoscaling"}, []string{"horizontalpodautoscalers"}, writeVerbs)
	add([]string{"batch"}, []string{"cronjobs", "jobs"}, writeVerbs)
	add([]string{"networking.k8s.io"}, []string{"ingresses", "networkpolicies"}, writeVerbs)
	add([]string{"policy"}, []string{"poddisruptionbudgets"}, writeVerbs)

	if e.ExecPods {
		add([]string{""}, []string{"pods/attach", "pods/exec", "pods/portforward"}, []string{"create"})
	}
	if e.ReadSecrets {
		add([]string{""}, []string{"secrets"}, []string{"get", "list", "watch"})
	}
	return rules
}

// Verbs returns the K8s verb list this EffectiveVerbs maps to.
func (e EffectiveVerbs) Verbs() []string {
	if e.Superuser {
		// Superuser bypasses the Role and goes through a
		// ClusterRoleBinding to cluster-admin.
		return []string{"*"}
	}
	seen := make(map[string]bool)
	for _, rule := range shellRules(e) {
		for _, verb := range rule["verbs"].([]string) {
			seen[verb] = true
		}
	}
	out := make([]string, 0, len(seen))
	for verb := range seen {
		out = append(out, verb)
	}
	sort.Strings(out)
	return out
}

// ServiceAccountManifest renders the SA JSON the tunnel POSTs to
// /api/v1/namespaces/{ns}/serviceaccounts.
func ServiceAccountManifest(n Names) []byte {
	m := map[string]any{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata": map[string]any{
			"name":      n.SAName,
			"namespace": n.SANamespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "kubectl-shell",
			},
		},
	}
	b, _ := json.Marshal(m)
	return b
}

// ClusterRoleManifest renders the caller-derived policy across namespaces.
//
// Superuser callers get nil here and fall back to the cluster-admin
// built-in binding.
func ClusterRoleManifest(n Names, verbs EffectiveVerbs) []byte {
	if verbs.Superuser {
		return nil
	}
	m := map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata": map[string]any{
			"name": n.RoleName,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "kubectl-shell",
			},
		},
		"rules": shellRules(verbs),
	}
	b, _ := json.Marshal(m)
	return b
}

// RoleManifest renders a NAMESPACED Role scoped to a single namespace,
// mirroring `verbs` against the wildcard resource set. Task 009 (DIR-04)
// mechanism A: when a caller's astronomer grants confine them to a
// specific set of namespaces, the shell provisions one Role + RoleBinding
// per authorized namespace instead of a cluster-wide ClusterRole, so the
// session ServiceAccount is genuinely confined at the apiserver — a
// namespace-scoped operator can no longer read across the whole cluster.
//
// Superuser callers never reach this path (they bind to cluster-admin).
func RoleManifest(n Names, namespace string, verbs EffectiveVerbs) []byte {
	if verbs.Superuser {
		return nil
	}
	m := map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "Role",
		"metadata": map[string]any{
			"name":      n.RoleName,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "kubectl-shell",
			},
		},
		"rules": shellRules(verbs),
	}
	b, _ := json.Marshal(m)
	return b
}

// RoleBindingManifest renders a NAMESPACED RoleBinding in `namespace` that
// ties the session ServiceAccount (which lives in n.SANamespace, i.e.
// kube-system) to the per-namespace Role emitted by RoleManifest. A
// RoleBinding may reference a subject in another namespace, so one SA is
// confined to exactly the set of namespaces the caller is authorized for.
func RoleBindingManifest(n Names, namespace string, verbs EffectiveVerbs) []byte {
	if verbs.Superuser {
		return nil
	}
	m := map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "RoleBinding",
		"metadata": map[string]any{
			"name":      n.BindingName,
			"namespace": namespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "kubectl-shell",
			},
		},
		"subjects": []map[string]any{
			{
				"kind":      "ServiceAccount",
				"name":      n.SAName,
				"namespace": n.SANamespace,
			},
		},
		"roleRef": map[string]any{
			"apiGroup": "rbac.authorization.k8s.io",
			"kind":     "Role",
			"name":     n.RoleName,
		},
	}
	b, _ := json.Marshal(m)
	return b
}

// ClusterRoleBindingManifest renders the binding that ties the SA to
// either the per-session ClusterRole (non-superuser) or the built-in
// cluster-admin ClusterRole (superuser).
func ClusterRoleBindingManifest(n Names, verbs EffectiveVerbs) []byte {
	roleRef := map[string]any{
		"apiGroup": "rbac.authorization.k8s.io",
		"kind":     "ClusterRole",
		"name":     n.RoleName,
	}
	if verbs.Superuser {
		roleRef["name"] = "cluster-admin"
	}
	m := map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRoleBinding",
		"metadata": map[string]any{
			"name": n.BindingName,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "kubectl-shell",
			},
		},
		"subjects": []map[string]any{
			{
				"kind":      "ServiceAccount",
				"name":      n.SAName,
				"namespace": n.SANamespace,
			},
		},
		"roleRef": roleRef,
	}
	b, _ := json.Marshal(m)
	return b
}

// PodManifest renders the debug pod JSON. The pod runs `sleep 4h` as a
// placeholder so `kubectl exec -it` can attach a shell on demand; the
// pod's SA is the one the manifest binds in ClusterRoleBindingManifest.
//
// image is the resolved registry path (operators can override the
// default via chart value kubectlShell.image).
func PodManifest(n Names, image string) []byte {
	if image == "" {
		image = DefaultImage
	}
	m := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      n.PodName,
			"namespace": n.PodNamespace,
			"labels": map[string]string{
				"app.kubernetes.io/managed-by": "astronomer-go",
				"astronomer.io/component":      "kubectl-shell",
			},
		},
		"spec": map[string]any{
			"serviceAccountName":            n.SAName,
			"restartPolicy":                 "Never",
			"terminationGracePeriodSeconds": int64(2),
			// The pod auto-suicides after 4 hours so an orphaned reaper
			// run still cleans up — defense in depth alongside the
			// reaper sweep.
			"activeDeadlineSeconds": int64(4 * 60 * 60),
			"containers": []map[string]any{
				{
					"name":            n.Container,
					"image":           image,
					"imagePullPolicy": "IfNotPresent",
					"command":         []string{"/bin/sh", "-c", "sleep 14400"},
					"stdin":           true,
					"tty":             true,
					"securityContext": map[string]any{
						"runAsNonRoot":             true,
						"runAsUser":                int64(1001),
						"allowPrivilegeEscalation": false,
						"capabilities": map[string]any{
							"drop": []string{"ALL"},
						},
						"readOnlyRootFilesystem": false,
					},
					"resources": map[string]any{
						"requests": map[string]any{
							"cpu":    "50m",
							"memory": "64Mi",
						},
						"limits": map[string]any{
							"cpu":    "500m",
							"memory": "256Mi",
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(m)
	return b
}
