// Package agent — impersonation capability self-probe.
//
// The agent half of the capability handshake in
// docs/design/downstream-impersonation.md §8 item 6. It answers ONE question,
// about the agent's own rights, and reports the answer in the heartbeat:
//
//	"May I impersonate the pinned astronomer proxy subject?"
//
// The server refuses to move a cluster to the `enforce` downstream-impersonation
// mode unless the answer has been observed as YES. That is what prevents both
// version skew (an old agent that would ignore the identity fields never
// advertises the feature, so it is never sent enforcing traffic) and the
// "flag on, every request 403s" failure the plan's acceptance criteria call out.
//
// PHASE 0: the answer is NO on every shipped profile, because no profile grants
// the `impersonate` verb and this phase deliberately does not add it (that grant
// is the design decision that has not been made). The probe is therefore a
// fail-closed gate that currently always closes — which is the correct and
// intended Phase 0 state, not a bug.
package agent

import (
	"context"
	"sync"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// impersonationProbeTTL is how long a probe result is trusted. An hour is short
// enough that an operator who applies the RBAC sees the capability advertised
// within one coffee break, and long enough that the probe is not a per-heartbeat
// apiserver call (heartbeats default to every 30s).
const impersonationProbeTTL = time.Hour

// impersonationProbe caches the SelfSubjectAccessReview result.
//
// SSAR create is granted to system:authenticated by the built-in
// system:basic-user ClusterRole, so this needs no rule from any astronomer
// privilege profile and adds no grant to deploy/agent/template.go.
type impersonationProbe struct {
	mu      sync.Mutex
	checked time.Time
	allowed bool
}

// allowedNow returns the cached answer, refreshing it when stale. Any error —
// no client, an apiserver that refuses the review, a context deadline — is
// reported as NOT allowed. Fail closed: an inconclusive probe must never be
// advertised as a capability.
func (p *impersonationProbe) allowedNow(ctx context.Context, client kubernetes.Interface) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.checked.IsZero() && time.Since(p.checked) < impersonationProbeTTL {
		return p.allowed
	}
	p.checked = time.Now()
	p.allowed = probeImpersonationAllowed(ctx, client)
	return p.allowed
}

// probeImpersonationAllowed runs the SelfSubjectAccessReview.
//
// The review asks about the NAMED subject protocol.ImpersonationProbeSubject
// rather than about unbounded impersonation. That is deliberate and is what
// makes the probe approach-independent: a resourceNames-bounded grant (the
// recommended Option D shape) answers YES to a named check and NO to an
// unbounded one, while an unbounded grant answers YES to both. Asking the named
// question therefore never under-reports an agent that actually holds the verb.
func probeImpersonationAllowed(ctx context.Context, client kubernetes.Interface) bool {
	if client == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	review, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Group:    "",
				Resource: "users",
				Verb:     "impersonate",
				Name:     protocol.ImpersonationProbeSubject,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil || review == nil {
		return false
	}
	return review.Status.Allowed && !review.Status.Denied
}

// applyImpersonationCapability appends protocol.FeatureImpersonation to exactly
// one of the heartbeat's two capability lists. Called AFTER the
// profile-derived fallback in collectHeartbeat, so it can never suppress that
// fallback by making the lists non-empty.
func applyImpersonationCapability(hb *protocol.HeartbeatPayload, allowed bool) {
	if hb == nil {
		return
	}
	if allowed {
		hb.EnabledFeatures = append(hb.EnabledFeatures, protocol.FeatureImpersonation)
		return
	}
	hb.DeniedFeatures = append(hb.DeniedFeatures, protocol.FeatureImpersonation)
}
