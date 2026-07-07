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

// EffectiveVerbs is the operator's verb set against the target cluster.
// Coarse on purpose — see docs/kubectl-shell.md. Boolean flags drive
// the rule set in the in-cluster Role:
//
//	clusters:read   ⇒ get, list, watch
//	clusters:update ⇒ + create, update, patch
//	clusters:delete ⇒ + delete
//	superuser       ⇒ cluster-admin binding (ClusterRoleBinding to
//	                  the built-in cluster-admin ClusterRole)
//
// v2 will mirror per-namespace bindings from the operator's project
// memberships. Keeping the v1 mapping coarse means operators who need
// finer grants fall back to the kubectl-proxy + kubeconfig flow.
type EffectiveVerbs struct {
	Read      bool
	Update    bool
	Delete    bool
	Superuser bool
}

// Verbs returns the K8s verb list this EffectiveVerbs maps to.
func (e EffectiveVerbs) Verbs() []string {
	if e.Superuser {
		// Superuser bypasses the Role and goes through a
		// ClusterRoleBinding to cluster-admin.
		return []string{"*"}
	}
	out := []string{}
	if e.Read || e.Update || e.Delete {
		out = append(out, "get", "list", "watch")
	}
	if e.Update || e.Delete {
		out = append(out, "create", "update", "patch")
	}
	if e.Delete {
		out = append(out, "delete")
	}
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

// ClusterRoleManifest renders the cluster-wide Role that mirrors
// `verbs` against the wildcard resource set. For v1 we use a single
// ClusterRole + ClusterRoleBinding so the operator can list resources
// across namespaces (matching the `kubectl get pods -A` flow most
// operators expect from a break-glass shell).
//
// Superuser callers get nil here and fall back to the cluster-admin
// built-in binding.
func ClusterRoleManifest(n Names, verbs EffectiveVerbs) []byte {
	if verbs.Superuser {
		return nil
	}
	rules := []map[string]any{
		{
			"apiGroups": []string{"*"},
			"resources": []string{"*"},
			"verbs":     verbs.Verbs(),
		},
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
		"rules": rules,
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
	rules := []map[string]any{
		{
			"apiGroups": []string{"*"},
			"resources": []string{"*"},
			"verbs":     verbs.Verbs(),
		},
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
		"rules": rules,
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
