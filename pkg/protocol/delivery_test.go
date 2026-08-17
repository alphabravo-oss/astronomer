package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validDeliveryAssignment(id string) DeliveryAssignmentV2 {
	return DeliveryAssignmentV2{
		DeploymentID: id,
		TargetID:     "22222222-2222-4222-8222-222222222222",
		ProjectID:    "33333333-3333-4333-8333-333333333333",
		Generation:   1,
		SpecDigest:   "sha256:" + strings.Repeat("a", 64),
		Action:       DeliveryActionApply,
		Scope:        DeliveryScopeNamespace,
		Source: DeliverySourceV2{
			Kind:     DeliverySourceGit,
			URL:      "ssh://git@example.com/platform/apps.git",
			Revision: strings.Repeat("b", 40),
			Path:     "clusters/base",
		},
		Renderer: DeliveryRendererV2{
			Kind: DeliveryRendererKustomize,
			Kustomize: &DeliveryKustomizeRenderer{
				TargetNamespace: "apps",
				ServiceAccount:  "delivery-applier",
				Prune:           true,
				Wait:            true,
			},
		},
		Policy: DeliveryReconciliationPolicy{Interval: "10m", Timeout: "10m", Drift: "repair", Prune: true},
	}
}

func validDeliveryResponse(t *testing.T) DeliveryStateResponseV2 {
	t.Helper()
	r := DeliveryStateResponseV2{
		ProtocolVersion:    DeliveryProtocolVersion,
		SnapshotGeneration: 1,
		FullSnapshot:       true,
		CredentialEpoch:    1,
		Assignments: []DeliveryAssignmentV2{
			validDeliveryAssignment("11111111-1111-4111-8111-111111111111"),
		},
	}
	var err error
	r.ETag, err = r.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDeliverySnapshotValidate(t *testing.T) {
	r := validDeliveryResponse(t)
	if err := r.Validate(); err != nil {
		t.Fatalf("valid response: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*DeliveryStateResponseV2)
	}{
		{"protocol", func(r *DeliveryStateResponseV2) { r.ProtocolVersion = "1" }},
		{"mutable git ref", func(r *DeliveryStateResponseV2) { r.Assignments[0].Source.Revision = "main" }},
		{"bad spec digest", func(r *DeliveryStateResponseV2) { r.Assignments[0].SpecDigest = "sha256:no" }},
		{"duplicate", func(r *DeliveryStateResponseV2) { r.Assignments = append(r.Assignments, r.Assignments[0]) }},
		{"bad etag", func(r *DeliveryStateResponseV2) { r.ETag = "sha256:" + strings.Repeat("0", 64) }},
		{"renderer union", func(r *DeliveryStateResponseV2) { r.Assignments[0].Renderer.Helm = &DeliveryHelmRenderer{} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := validDeliveryResponse(t)
			tc.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDeliverySnapshotETagIsOrderStableAndSecretFree(t *testing.T) {
	a := validDeliveryAssignment("11111111-1111-4111-8111-111111111111")
	b := validDeliveryAssignment("44444444-4444-4444-8444-444444444444")
	a.Credential = &DeliveryCredentialMaterial{Version: 1, Data: map[string][]byte{"token": []byte("first-secret")}}
	b.Credential = &DeliveryCredentialMaterial{Version: 1, Data: map[string][]byte{"token": []byte("second-secret")}}

	first := DeliveryStateResponseV2{ProtocolVersion: DeliveryProtocolVersion, SnapshotGeneration: 7, CredentialEpoch: 2, Assignments: []DeliveryAssignmentV2{a, b}}
	second := DeliveryStateResponseV2{ProtocolVersion: DeliveryProtocolVersion, SnapshotGeneration: 7, CredentialEpoch: 2, Assignments: []DeliveryAssignmentV2{b, a}}
	firstETag, err := first.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	secondETag, err := second.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	if firstETag != secondETag {
		t.Fatalf("ETag changed with order: %s != %s", firstETag, secondETag)
	}

	second.Assignments[0].Credential.Data["token"] = []byte("rotated-secret")
	secretOnlyETag, err := second.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	if secretOnlyETag != firstETag {
		t.Fatal("credential bytes must not affect ETag")
	}
	second.CredentialEpoch++
	rotatedETag, err := second.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	if rotatedETag == firstETag {
		t.Fatal("credential epoch must affect ETag")
	}
}

func TestDeliveryCredentialBounds(t *testing.T) {
	r := validDeliveryResponse(t)
	r.Assignments[0].Credential = &DeliveryCredentialMaterial{
		Version: 1,
		Data:    map[string][]byte{"token": make([]byte, MaxDeliveryCredentialValue+1)},
	}
	if err := r.Assignments[0].Validate(); err == nil {
		t.Fatal("expected oversized credential rejection")
	}
}

func TestDeliveryStateRequestValidate(t *testing.T) {
	request := DeliveryStateRequestV2{
		ClusterID:               "11111111-1111-4111-8111-111111111111",
		ProtocolVersion:         DeliveryProtocolVersion,
		AckedSnapshotGeneration: 1,
		AckedETag:               "sha256:" + strings.Repeat("a", 64),
		ControllerInventory: DeliveryControllerInventory{
			FluxVersion:        "v2.9.3",
			Components:         map[string]string{"source-controller": "v1.9.3"},
			APIVersions:        []string{"source.toolkit.fluxcd.io/v1"},
			DistributionDigest: "sha256:" + strings.Repeat("b", 64),
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	request.ControllerInventory.Components["NOT_DNS"] = "v1"
	if err := request.Validate(); err == nil {
		t.Fatal("expected invalid inventory component rejection")
	}
}

func TestDeliveryStatusValidate(t *testing.T) {
	status := DeliveryStatusV2{
		ProtocolVersion:    DeliveryProtocolVersion,
		ClusterID:          "11111111-1111-4111-8111-111111111111",
		SessionSequence:    2,
		SnapshotGeneration: 3,
		SnapshotETag:       "sha256:" + strings.Repeat("d", 64),
		Deployments: []DeliveryDeploymentStatusV2{{
			DeploymentID: "22222222-2222-4222-8222-222222222222",
			Generation:   4,
			SpecDigest:   "sha256:" + strings.Repeat("c", 64),
			Phase:        "ready",
			Conditions: []DeliveryCondition{{
				Type:               "Ready",
				Status:             "True",
				ObservedGeneration: 4,
			}},
			Inventory:  DeliveryInventory{Entries: 2, Ready: 2},
			ObservedAt: time.Now().UTC(),
		}},
	}
	if err := status.Validate(); err != nil {
		t.Fatalf("valid status: %v", err)
	}
	status.Deployments[0].Conditions[0].Message = strings.Repeat("x", MaxDeliveryStatusMessageBytes+1)
	if err := status.Validate(); err == nil {
		t.Fatal("expected oversized condition rejection")
	}
}

func FuzzDeliveryStateResponseValidate(f *testing.F) {
	response := DeliveryStateResponseV2{
		ProtocolVersion:    DeliveryProtocolVersion,
		SnapshotGeneration: 1,
		FullSnapshot:       true,
		CredentialEpoch:    1,
		Assignments: []DeliveryAssignmentV2{
			validDeliveryAssignment("11111111-1111-4111-8111-111111111111"),
		},
	}
	response.ETag, _ = response.CanonicalETag()
	seed, _ := json.Marshal(response)
	f.Add(seed)
	f.Fuzz(func(t *testing.T, raw []byte) {
		var response DeliveryStateResponseV2
		if json.Unmarshal(raw, &response) == nil {
			_ = response.Validate()
		}
	})
}
