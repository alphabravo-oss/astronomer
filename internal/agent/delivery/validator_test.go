package delivery

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestValidateSnapshotBeforeSideEffects(t *testing.T) {
	t.Parallel()
	valid := gitAssignment()
	snapshot := protocol.DeliveryStateResponseV2{
		ProtocolVersion: protocol.DeliveryProtocolVersion, SnapshotGeneration: 1, FullSnapshot: true,
		Assignments: []protocol.DeliveryAssignmentV2{valid}, CredentialEpoch: 3,
	}
	etag, err := snapshot.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ETag = etag
	if err := ValidateSnapshot(snapshot, testCapabilities(), ValidationPolicy{}); err != nil {
		t.Fatalf("ValidateSnapshot() error = %v", err)
	}

	unsafe := testCapabilities()
	unsafe.NoRemoteKustomizeBases = false
	if err := ValidateSnapshot(snapshot, unsafe, ValidationPolicy{}); err == nil || !strings.Contains(err.Error(), "remote-base isolation") {
		t.Fatalf("unsafe controller capability error = %v", err)
	}
	snapshot.Assignments[0].Renderer.Kustomize.TargetNamespace = DeliverySystemNamespace
	etag, _ = snapshot.CanonicalETag()
	snapshot.ETag = etag
	if err := ValidateSnapshot(snapshot, testCapabilities(), ValidationPolicy{}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved namespace error = %v", err)
	}
}

func TestValidateAssignmentFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*protocol.DeliveryAssignmentV2, *Capabilities, *ValidationPolicy)
		match  string
	}{
		{name: "service-account-injection", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) {
			a.Renderer.Kustomize.ServiceAccount = "cluster-admin"
		}, match: "service account"},
		{name: "renderer-source-confusion", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) {
			a.Source = helmHTTPAssignment().Source
		}, match: "cannot consume"},
		{name: "unknown-credential-key", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) {
			a.Credential.Data["evil"] = []byte("x")
		}, match: "not an allowlisted"},
		{name: "missing-auth-material", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) {
			delete(a.Credential.Data, "identity")
			delete(a.Credential.Data, "known_hosts")
		}, match: "no authentication material"},
		{name: "cross-namespace-health", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) {
			a.Renderer.Kustomize.HealthChecks[0].Namespace = "other"
		}, match: "escapes"},
		{name: "system-dependency", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) {
			a.Renderer.Kustomize.DependencyNames = []string{"astronomer-system"}
		}, match: "not a deterministic"},
		{name: "invalid-patch", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) {
			a.Renderer.Kustomize.Patches = []string{"{{{"}
		}, match: "not one valid"},
		{name: "unadvertised-source", mutate: func(_ *protocol.DeliveryAssignmentV2, c *Capabilities, _ *ValidationPolicy) {
			c.SourceKinds = []protocol.DeliverySourceKind{protocol.DeliverySourceOCIArtifact}
		}, match: "not advertised"},
		{name: "unadvertised-api", mutate: func(_ *protocol.DeliveryAssignmentV2, c *Capabilities, _ *ValidationPolicy) {
			c.FluxAPIVersions = []string{"source.toolkit.fluxcd.io/v1"}
		}, match: "Flux API"},
		{name: "zero-interval", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) { a.Policy.Interval = "0s" }, match: "between 5s"},
		{name: "prune-policy-confusion", mutate: func(a *protocol.DeliveryAssignmentV2, _ *Capabilities, _ *ValidationPolicy) {
			a.Renderer.Kustomize.Prune = false
		}, match: "prune setting"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assignment, capabilities, policy := gitAssignment(), testCapabilities(), ValidationPolicy{}
			test.mutate(&assignment, &capabilities, &policy)
			err := ValidateAssignment(assignment, capabilities, policy)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("ValidateAssignment() error = %v, want containing %q", err, test.match)
			}
		})
	}
}

func TestValidatePlatformRequiresTwoIndependentGates(t *testing.T) {
	t.Parallel()
	assignment := helmOCIAssignment()
	capabilities := testCapabilities()
	if err := ValidateAssignment(assignment, capabilities, ValidationPolicy{}); err == nil {
		t.Fatal("platform assignment passed without explicit release authorization")
	}
	capabilities.PlatformScope = false
	if err := ValidateAssignment(assignment, capabilities, ValidationPolicy{AllowPlatformScope: true}); err == nil {
		t.Fatal("platform assignment passed without cluster capability")
	}
	capabilities.PlatformScope = true
	if err := ValidateAssignment(assignment, capabilities, ValidationPolicy{AllowPlatformScope: true}); err != nil {
		t.Fatalf("authorized platform assignment rejected: %v", err)
	}
}

func FuzzValidateAssignment(f *testing.F) {
	seed, _ := json.Marshal(gitAssignment())
	f.Add(seed)
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > MaxAssignmentBytes+1 {
			return
		}
		var assignment protocol.DeliveryAssignmentV2
		if json.Unmarshal(payload, &assignment) != nil {
			return
		}
		_ = ValidateAssignment(assignment, testCapabilities(), ValidationPolicy{AllowPlatformScope: true})
	})
}
