// Package mesh detects which service mesh (Istio / Linkerd / Kuma /
// Cilium-mesh) is installed on a member cluster and returns aggregate
// health counts that drive the cluster-detail "Service mesh" tab.
//
// The detector is intentionally read-only: it never mutates the
// cluster. The only side-effect is the row upserted into
// cluster_service_mesh by the calling worker / handler. The detector
// itself returns a value, never writes.
//
// Sprint 071 surface:
//   - Detect inspects a cluster via a K8sRequester (the existing
//     tunnel-backed handler.K8sRequester is an exact match). It runs
//     6–8 small `kubectl get` style requests; on a healthy member
//     cluster the total round-trip is under a second.
//
// Detection precedence (when multiple meshes have artifacts present):
//   - Istio   wins first when istiod is in istio-system OR a Gateway /
//             VirtualService / DestinationRule CR exists. The Istio
//             ecosystem is the dominant install across the fleet and
//             its CRDs collide with no other mesh's API surface.
//   - Linkerd second when the linkerd-control-plane Deployment exists
//             OR a ServiceProfile / Server CR exists.
//   - Kuma    third (kuma-system control-plane Deployment).
//   - Cilium  last (cilium ClusterMesh signal). The cluster-mesh
//             detection is light-weight because the full Cilium agent
//             is a node-level thing; here we only look for the
//             cluster-mesh feature being enabled.
//   - "none"  is the catch-all when every probe returns 0/404.
//
// The detector tolerates partial failure: any one of the six probes
// erroring is logged into Detection.Errors (caller surfaces in the
// last_error column) but the remaining probes still run so the UI
// can show partial counts. Detection itself returns nil only when
// the K8sRequester is unreachable entirely (cluster offline).
package mesh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Mesh values stamped into cluster_service_mesh.detected_mesh. Kept as
// untyped strings so the DB CHECK constraint is the canonical source
// of truth for the enum.
const (
	MeshIstio   = "istio"
	MeshLinkerd = "linkerd"
	MeshKuma    = "kuma"
	MeshCilium  = "cilium"
	MeshNone    = "none"
	MeshUnknown = "unknown"
)

// K8sRequester is the local mirror of handler.K8sRequester /
// tasks.K8sRequester. Declared here so internal/mesh has no import
// dependency on handler or worker.
type K8sRequester interface {
	Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error)
}

// Querier is the optional DB surface for sprint-069 mirrored CRDs. The
// detector consults it first for Gateway / VirtualService / etc.
// counts when available (cheap one-shot SELECT against a local
// mirror), then falls back to the tunnel-backed K8sRequester. The
// 071-only build has no mirrored counts wired in, so this interface
// is satisfiable by a no-op stub — every method may return (0, nil)
// without invalidating the detector.
type Querier interface {
	// CountMirroredKind returns the number of rows in the sprint-069
	// CRD mirror for (clusterID, group, kind). When sprint 069 didn't
	// ship the kind, implementations return (0, false) — the second
	// return is the "have mirror?" bit, not an error. The detector
	// then falls back to the tunnel probe.
	CountMirroredKind(ctx context.Context, clusterID uuid.UUID, group, kind string) (count int, mirrored bool)
}

// Detection is the populated detection result.
//
// All counts are best-effort: a non-fatal probe error leaves the
// corresponding field at zero and appends to Errors. Mesh + Version
// reflect the highest-precedence mesh whose control-plane signal
// fired; when nothing fires Mesh = "none".
type Detection struct {
	Mesh                  string `json:"mesh"`
	Version               string `json:"version"`
	ControlPlaneNamespace string `json:"control_plane_namespace"`
	GatewayCount          int    `json:"gateway_count"`
	VirtualServiceCount   int    `json:"virtual_service_count"`
	DestinationRuleCount  int    `json:"destination_rule_count"`
	PeerAuthCount         int    `json:"peer_auth_count"`
	ServiceProfileCount   int    `json:"service_profile_count"`
	ServerAuthCount       int    `json:"server_auth_count"`
	MTLSCoveragePct       int    `json:"mtls_coverage_pct"`
	// Errors carries partial-failure messages so the calling row can
	// stamp last_error without losing the counts the other probes
	// produced. Empty slice means "every probe ran cleanly".
	Errors []string `json:"errors,omitempty"`
}

// noopQuerier is the default mirrored-CRD source used when the
// detector is called without one (e.g. sprint 069 isn't in this
// build). Every CountMirroredKind returns (0, false) — fall-back to
// the tunnel probe is implicit.
type noopQuerier struct{}

func (noopQuerier) CountMirroredKind(_ context.Context, _ uuid.UUID, _ string, _ string) (int, bool) {
	return 0, false
}

// Detect inspects the cluster and returns the best-effort detection.
// It NEVER mutates the cluster.
//
// q may be nil — the detector falls back to a noop querier and runs
// every probe directly against the tunnel. The K8sRequester req is
// required; passing nil returns an error.
func Detect(ctx context.Context, q Querier, req K8sRequester, clusterID uuid.UUID) (Detection, error) {
	if req == nil {
		return Detection{Mesh: MeshUnknown}, fmt.Errorf("mesh: K8sRequester is required")
	}
	if q == nil {
		q = noopQuerier{}
	}
	out := Detection{Mesh: MeshNone}
	clusterStr := clusterID.String()

	// Pull the namespace list once; multiple probes need it (mTLS
	// coverage denominator + Linkerd Server lookup). A 503 here means
	// the cluster is unreachable; bail with a clear error so the
	// worker can stamp last_error.
	namespaces, nsErr := listNamespaces(ctx, req, clusterStr)
	if nsErr != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("list namespaces: %v", nsErr))
	}

	// Istio probes -----------------------------------------------------
	gateways := probeCount(ctx, q, req, clusterID, clusterStr,
		"networking.istio.io", "Gateway",
		"/apis/networking.istio.io/v1beta1/gateways", &out.Errors)
	out.GatewayCount = gateways
	vs := probeCount(ctx, q, req, clusterID, clusterStr,
		"networking.istio.io", "VirtualService",
		"/apis/networking.istio.io/v1beta1/virtualservices", &out.Errors)
	out.VirtualServiceCount = vs
	dr := probeCount(ctx, q, req, clusterID, clusterStr,
		"networking.istio.io", "DestinationRule",
		"/apis/networking.istio.io/v1beta1/destinationrules", &out.Errors)
	out.DestinationRuleCount = dr
	peerAuth, peerAuthByNS := probePeerAuth(ctx, req, clusterStr, &out.Errors)
	out.PeerAuthCount = peerAuth

	// Linkerd probes ---------------------------------------------------
	sps := probeCount(ctx, q, req, clusterID, clusterStr,
		"linkerd.io", "ServiceProfile",
		"/apis/linkerd.io/v1alpha2/serviceprofiles", &out.Errors)
	out.ServiceProfileCount = sps
	servers := probeCount(ctx, q, req, clusterID, clusterStr,
		"policy.linkerd.io", "Server",
		"/apis/policy.linkerd.io/v1beta1/servers", &out.Errors)
	out.ServerAuthCount = servers

	// Control-plane / version probes ----------------------------------
	istiodVersion, istiodNS := detectIstiod(ctx, req, clusterStr)
	linkerdVersion, linkerdNS := detectLinkerd(ctx, req, clusterStr)
	kumaPresent, kumaNS := detectKuma(ctx, req, clusterStr)
	ciliumMeshPresent, ciliumNS := detectCiliumMesh(ctx, req, clusterStr)

	// Precedence: Istio → Linkerd → Kuma → Cilium → none.
	istioSignal := istiodVersion != "" || gateways > 0 || vs > 0 || dr > 0 || peerAuth > 0
	linkerdSignal := linkerdVersion != "" || sps > 0 || servers > 0
	switch {
	case istioSignal:
		out.Mesh = MeshIstio
		out.Version = istiodVersion
		out.ControlPlaneNamespace = pickNonEmpty(istiodNS, "istio-system")
	case linkerdSignal:
		out.Mesh = MeshLinkerd
		out.Version = linkerdVersion
		out.ControlPlaneNamespace = pickNonEmpty(linkerdNS, "linkerd")
	case kumaPresent:
		out.Mesh = MeshKuma
		out.ControlPlaneNamespace = pickNonEmpty(kumaNS, "kuma-system")
	case ciliumMeshPresent:
		out.Mesh = MeshCilium
		out.ControlPlaneNamespace = pickNonEmpty(ciliumNS, "kube-system")
	default:
		out.Mesh = MeshNone
	}

	out.MTLSCoveragePct = mtlsCoverage(out.Mesh, namespaces, peerAuthByNS, servers)
	return out, nil
}

// probeCount returns the count of a kind. The mirrored querier wins
// when present (sprint 069), otherwise the tunnel listing path runs.
// Tunnel failures are appended to errs (the caller's slice) and the
// returned count is 0.
func probeCount(ctx context.Context, q Querier, req K8sRequester, clusterID uuid.UUID, clusterStr, group, kind, listPath string, errs *[]string) int {
	if count, mirrored := q.CountMirroredKind(ctx, clusterID, group, kind); mirrored {
		return count
	}
	resp, err := req.Do(ctx, clusterStr, http.MethodGet, listPath, nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("list %s.%s: %v", group, kind, err))
		return 0
	}
	if resp == nil {
		return 0
	}
	if resp.StatusCode == http.StatusNotFound {
		// CRD not installed — silent zero, no error.
		return 0
	}
	if resp.StatusCode >= http.StatusBadRequest {
		*errs = append(*errs, fmt.Sprintf("list %s.%s status=%d", group, kind, resp.StatusCode))
		return 0
	}
	body, err := decodeBody(resp)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("decode %s.%s body: %v", group, kind, err))
		return 0
	}
	var envelope struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		*errs = append(*errs, fmt.Sprintf("unmarshal %s.%s: %v", group, kind, err))
		return 0
	}
	return len(envelope.Items)
}

// probePeerAuth lists Istio PeerAuthentications across all namespaces
// and counts both the total and the per-namespace map of strict-mode
// rules used for the mTLS coverage heuristic.
//
// A 404 (CRD absent) returns (0, nil) silently — peer auth simply
// doesn't apply on a non-Istio cluster.
func probePeerAuth(ctx context.Context, req K8sRequester, clusterStr string, errs *[]string) (int, map[string]int) {
	resp, err := req.Do(ctx, clusterStr, http.MethodGet,
		"/apis/security.istio.io/v1beta1/peerauthentications", nil,
		map[string]string{"Accept": "application/json"})
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("list peerauthentications: %v", err))
		return 0, map[string]int{}
	}
	if resp == nil || resp.StatusCode == http.StatusNotFound {
		return 0, map[string]int{}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		*errs = append(*errs, fmt.Sprintf("list peerauthentications status=%d", resp.StatusCode))
		return 0, map[string]int{}
	}
	body, err := decodeBody(resp)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("decode peerauthentications: %v", err))
		return 0, map[string]int{}
	}
	var envelope struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				MTLS struct {
					Mode string `json:"mode"`
				} `json:"mtls"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		*errs = append(*errs, fmt.Sprintf("unmarshal peerauthentications: %v", err))
		return 0, map[string]int{}
	}
	byNS := map[string]int{}
	for _, it := range envelope.Items {
		if it.Spec.MTLS.Mode == "STRICT" {
			byNS[it.Metadata.Namespace]++
		}
	}
	return len(envelope.Items), byNS
}

// istioImageRe captures the istiod image tag — e.g. `istiod:1.22.4` or
// `docker.io/istio/pilot:1.22.4-distroless`. Used by detectIstiod.
var istioImageRe = regexp.MustCompile(`(?i)istio[^:]*[:/]([0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?)`)

// detectIstiod returns (version, namespace). The version is parsed
// from the istiod Deployment image tag; when the Deployment isn't
// found we return "" (no Istio control plane signal).
//
// Namespace search order: the istio-system convention, then a
// labelled-Deployments fallback.
func detectIstiod(ctx context.Context, req K8sRequester, clusterStr string) (string, string) {
	for _, ns := range []string{"istio-system"} {
		ver := readDeploymentVersion(ctx, req, clusterStr, ns, "istiod", istioImageRe)
		if ver != "" {
			return ver, ns
		}
	}
	// Fallback: scan Deployments labelled app=istiod across the cluster.
	resp, err := req.Do(ctx, clusterStr, http.MethodGet,
		"/apis/apps/v1/deployments?labelSelector=app%3Distiod", nil,
		map[string]string{"Accept": "application/json"})
	if err != nil || resp == nil || resp.StatusCode >= http.StatusBadRequest {
		return "", ""
	}
	body, err := decodeBody(resp)
	if err != nil {
		return "", ""
	}
	return extractFirstVersion(body, istioImageRe)
}

// linkerdImageRe captures Linkerd images — the destination /
// identity / proxy-injector Deployments all run the same versioned
// image tag.
var linkerdImageRe = regexp.MustCompile(`(?i)linkerd[^:]*[:/](?:stable-|edge-)?([0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?)`)

func detectLinkerd(ctx context.Context, req K8sRequester, clusterStr string) (string, string) {
	for _, ns := range []string{"linkerd"} {
		ver := readDeploymentVersion(ctx, req, clusterStr, ns, "linkerd-destination", linkerdImageRe)
		if ver != "" {
			return ver, ns
		}
	}
	resp, err := req.Do(ctx, clusterStr, http.MethodGet,
		"/apis/apps/v1/deployments?labelSelector=linkerd.io%2Fcontrol-plane-component", nil,
		map[string]string{"Accept": "application/json"})
	if err != nil || resp == nil || resp.StatusCode >= http.StatusBadRequest {
		return "", ""
	}
	body, err := decodeBody(resp)
	if err != nil {
		return "", ""
	}
	return extractFirstVersion(body, linkerdImageRe)
}

// detectKuma returns (present, namespace). Kuma's control plane is
// the kuma-control-plane Deployment in kuma-system by convention.
func detectKuma(ctx context.Context, req K8sRequester, clusterStr string) (bool, string) {
	for _, ns := range []string{"kuma-system"} {
		resp, err := req.Do(ctx, clusterStr, http.MethodGet,
			fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/kuma-control-plane", ns), nil,
			map[string]string{"Accept": "application/json"})
		if err == nil && resp != nil && resp.StatusCode < http.StatusBadRequest {
			return true, ns
		}
	}
	return false, ""
}

// detectCiliumMesh returns (present, namespace) for Cilium ClusterMesh.
// Cilium itself runs in kube-system; we look for the clustermesh-apiserver
// Deployment which only exists when ClusterMesh is enabled.
func detectCiliumMesh(ctx context.Context, req K8sRequester, clusterStr string) (bool, string) {
	for _, ns := range []string{"kube-system"} {
		resp, err := req.Do(ctx, clusterStr, http.MethodGet,
			fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/clustermesh-apiserver", ns), nil,
			map[string]string{"Accept": "application/json"})
		if err == nil && resp != nil && resp.StatusCode < http.StatusBadRequest {
			return true, ns
		}
	}
	return false, ""
}

// readDeploymentVersion fetches one Deployment and pulls the version
// out of the first container image tag using imageRe. Returns "" if
// the Deployment doesn't exist or the regex doesn't match.
func readDeploymentVersion(ctx context.Context, req K8sRequester, clusterStr, namespace, name string, imageRe *regexp.Regexp) string {
	resp, err := req.Do(ctx, clusterStr, http.MethodGet,
		fmt.Sprintf("/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name), nil,
		map[string]string{"Accept": "application/json"})
	if err != nil || resp == nil || resp.StatusCode >= http.StatusBadRequest {
		return ""
	}
	body, err := decodeBody(resp)
	if err != nil {
		return ""
	}
	ver, _ := extractFirstVersion(body, imageRe)
	return ver
}

// extractFirstVersion walks any JSON blob looking for a string field
// named "image" and runs imageRe over each value, returning the first
// captured version. Returns (version, namespace) when the blob is an
// envelope with items; namespace is "" when the blob is a single
// Deployment.
func extractFirstVersion(body []byte, imageRe *regexp.Regexp) (string, string) {
	// Single-object short-circuit: most callers fetch a single
	// Deployment, so try that shape first.
	var single struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Template struct {
				Spec struct {
					Containers []struct {
						Image string `json:"image"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(body, &single); err == nil {
		for _, c := range single.Spec.Template.Spec.Containers {
			if m := imageRe.FindStringSubmatch(c.Image); len(m) > 1 {
				return m[1], single.Metadata.Namespace
			}
		}
	}
	// List shape: pull the first item with a matching image.
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err == nil {
		for _, it := range list.Items {
			for _, c := range it.Spec.Template.Spec.Containers {
				if m := imageRe.FindStringSubmatch(c.Image); len(m) > 1 {
					return m[1], it.Metadata.Namespace
				}
			}
		}
	}
	return "", ""
}

// listNamespaces returns the names of non-system namespaces, used as
// the mTLS coverage denominator. System namespaces (kube-system,
// kube-public, kube-node-lease, plus the well-known mesh control
// planes themselves) are excluded so an empty cluster doesn't appear
// to have 100% coverage just because the only namespaces are
// kube-system.
func listNamespaces(ctx context.Context, req K8sRequester, clusterStr string) ([]string, error) {
	resp, err := req.Do(ctx, clusterStr, http.MethodGet, "/api/v1/namespaces", nil,
		map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("nil response")
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("list namespaces status=%d", resp.StatusCode)
	}
	body, err := decodeBody(resp)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(envelope.Items))
	for _, it := range envelope.Items {
		name := it.Metadata.Name
		if isSystemNamespace(name) {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

// isSystemNamespace returns true for namespaces excluded from the
// mTLS coverage denominator. The list is conservative — we'd rather
// over-count user namespaces (and dilute the percentage) than hide a
// real gap by excluding too much.
func isSystemNamespace(name string) bool {
	switch name {
	case "kube-system", "kube-public", "kube-node-lease",
		"istio-system", "linkerd", "linkerd-viz", "kuma-system",
		"cattle-system", "cert-manager", "ingress-nginx":
		return true
	}
	return strings.HasPrefix(name, "kube-")
}

// mtlsCoverage returns the rough percentage of non-system namespaces
// covered by an mTLS-enforcing rule.
//
//   - Istio:   namespaces with at least one mode=STRICT
//              PeerAuthentication count as covered.
//   - Linkerd: presence of Server CRs cluster-wide implies coverage
//              equal to (server_count > 0 ? 100 : 0). Linkerd's
//              proxy auth doesn't decompose neatly by namespace
//              without a full Server↔Workload walk; the heuristic
//              keeps it cheap and "non-zero" when any Server exists.
//   - Other:   0%.
//
// Returns an int in [0, 100]. When the denominator is zero (no user
// namespaces yet) we return 0 — the UI renders "—" rather than 100%.
func mtlsCoverage(mesh string, userNamespaces []string, peerAuthByNS map[string]int, linkerdServers int) int {
	if len(userNamespaces) == 0 {
		return 0
	}
	switch mesh {
	case MeshIstio:
		if len(peerAuthByNS) == 0 {
			return 0
		}
		covered := 0
		for _, ns := range userNamespaces {
			if peerAuthByNS[ns] > 0 {
				covered++
			}
		}
		return covered * 100 / len(userNamespaces)
	case MeshLinkerd:
		if linkerdServers > 0 {
			return 100
		}
		return 0
	default:
		return 0
	}
}

// decodeBody base64-decodes the response body. The tunnel wraps every
// response body in base64; the handler / detector unwrap it before
// JSON-parsing.
func decodeBody(resp *protocol.K8sResponsePayload) ([]byte, error) {
	if resp == nil || resp.Body == "" {
		return []byte{}, nil
	}
	b, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		// Some test paths bypass base64 and stuff raw JSON straight
		// into Body. Tolerate that — if the decode fails, treat the
		// string as already-clean bytes.
		return []byte(resp.Body), nil
	}
	return b, nil
}

func pickNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
