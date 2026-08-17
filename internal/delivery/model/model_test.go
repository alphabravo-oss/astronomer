package model

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSourceValidation(t *testing.T) {
	t.Parallel()

	valid := Source{
		ID: uuid.New(), ProjectID: uuid.New(), Name: "platform-config", Type: SourceGit,
		URL: "ssh://git@github.example/platform/config.git", AuthMode: AuthSSH,
		Trust: TrustPolicy{Provider: SignatureGit, Identity: "release@example.test", KeyRef: "platform-release"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid source: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Source)
		code   ValidationCode
	}{
		{"zero ID", func(source *Source) { source.ID = uuid.Nil }, CodeRequired},
		{"HTTP Git", func(source *Source) { source.URL = "http://github.example/config.git" }, CodeInvalid},
		{"embedded password", func(source *Source) { source.URL = "ssh://git:secret@github.example/config.git" }, CodeSecretNotAllowed},
		{"query credential", func(source *Source) { source.URL = "ssh://git@github.example/config.git?token=secret" }, CodeInvalid},
		{"SSH on OCI", func(source *Source) { source.Type = SourceOCIArtifact }, CodeConflict},
		{"unsigned conflict", func(source *Source) { source.Trust.AllowUnsigned = true }, CodeConflict},
		{"unknown auth", func(source *Source) { source.AuthMode = "passwordless-magic" }, CodeUnsupported},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			test.mutate(&candidate)
			var validation *ValidationError
			if err := candidate.Validate(); !errors.As(err, &validation) || !validation.HasCode(test.code) {
				t.Fatalf("Validate() error = %v, want code %s", err, test.code)
			}
		})
	}
}

func TestImmutableRevisionValidation(t *testing.T) {
	t.Parallel()

	digest := Digest("sha256:" + strings.Repeat("a", 64))
	for _, revision := range []ImmutableRevision{
		{Kind: RevisionGitCommit, Value: strings.Repeat("b", 40), ArtifactDigest: digest},
		{Kind: RevisionGitCommit, Value: strings.Repeat("c", 64), ArtifactDigest: digest},
		{Kind: RevisionOCIDigest, Value: digest.String(), ArtifactDigest: digest},
		{Kind: RevisionHelmChart, Value: "4.12.1", ArtifactDigest: digest},
	} {
		if err := revision.Validate(); err != nil {
			t.Errorf("valid revision %+v: %v", revision, err)
		}
	}
	for _, value := range []string{"main", "v1.2.x", "ABCDEF", strings.Repeat("a", 39)} {
		if err := (ImmutableRevision{Kind: RevisionGitCommit, Value: value, ArtifactDigest: digest}).Validate(); err == nil {
			t.Errorf("mutable/invalid Git revision %q unexpectedly valid", value)
		}
	}
}

func TestProviderFacingSpecsHaveStableNonSecretJSON(t *testing.T) {
	t.Parallel()

	source := ResolvedSourceSpec{
		SourceID: uuid.MustParse("10000000-0000-0000-0000-000000000001"),
		Type:     SourceOCIArtifact, URL: "oci://registry.example.test/platform/config", AuthMode: AuthBearer,
		Trust:    TrustPolicy{Provider: SignatureCosignKeyless, Identity: "https://github.com/example/repo/.github/workflows/release.yml@refs/tags/v1.2.3", Issuer: "https://token.actions.githubusercontent.com"},
		Revision: ImmutableRevision{Kind: RevisionOCIDigest, Value: "sha256:" + strings.Repeat("a", 64), ArtifactDigest: Digest("sha256:" + strings.Repeat("a", 64))},
	}
	if err := source.Validate(); err != nil {
		t.Fatal(err)
	}
	renderer := RendererSpec{Kind: RendererKustomize, Kustomize: &KustomizeSpec{Path: "./clusters/prod", TargetNamespace: "platform", Patches: []string{"patch-a"}}}
	if err := renderer.Validate(); err != nil {
		t.Fatal(err)
	}
	policy := ReconciliationPolicy{Interval: Duration(10 * time.Minute), RetryInterval: Duration(time.Minute), Timeout: Duration(5 * time.Minute), Prune: true, Wait: true, Drift: DriftRepair}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(struct {
		Source   ResolvedSourceSpec   `json:"source_spec"`
		Renderer RendererSpec         `json:"renderer_spec"`
		Policy   ReconciliationPolicy `json:"reconciliation_policy"`
	}{source, renderer, policy})
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, required := range []string{
		`"source_spec":{"source_id":"10000000-0000-0000-0000-000000000001","type":"oci_artifact","url":"oci://registry.example.test/platform/config","auth_mode":"bearer","trust_policy":`,
		`"revision":{"kind":"oci_digest","value":"sha256:`,
		`"renderer_spec":{"kind":"kustomize","kustomize":{"path":"./clusters/prod","target_namespace":"platform","patches":["patch-a"]}}`,
		`"reconciliation_policy":{"interval":"10m0s","retry_interval":"1m0s","timeout":"5m0s","prune":true,"wait":true,"drift":"repair"}`,
	} {
		if !strings.Contains(got, required) {
			t.Errorf("stable JSON missing %s: %s", required, got)
		}
	}
	for _, forbidden := range []string{"credential", "password", "token", "private_key"} {
		if strings.Contains(got, `"`+forbidden+`"`) {
			t.Errorf("source spec unexpectedly exposes secret field %q: %s", forbidden, got)
		}
	}
}

func TestBundleSpecDigestCanonicalAndValidated(t *testing.T) {
	t.Parallel()

	digest := Digest("sha256:" + strings.Repeat("d", 64))
	sourceID := uuid.New()
	one := BundleVersionSpec{
		SourceID: sourceID, RequestedRevision: "4.12.1",
		Revision: ImmutableRevision{Kind: RevisionHelmChart, Value: "4.12.1", ArtifactDigest: digest},
		Renderer: RendererSpec{Kind: RendererHelm, Helm: &HelmSpec{
			Chart: "ingress-nginx", ChartVersion: "4.12.1", ReleaseName: "ingress-nginx",
			TargetNamespace: "ingress-nginx", Values: json.RawMessage(`{"z":1,"a":{"enabled":true}}`),
		}}, Scope: ScopeNamespace,
		Reconciliation: ReconciliationPolicy{
			Interval: Duration(10 * time.Minute), RetryInterval: Duration(time.Minute),
			Timeout: Duration(10 * time.Minute), Prune: true, Wait: true, Drift: DriftRepair,
		},
		RequiredCapabilities: []CapabilityRequirement{{Name: "delivery.astronomer.io/helm", Constraint: ">=2.0.0"}, {Name: "delivery.astronomer.io/scope.namespace"}},
	}
	two := one
	two.Renderer.Helm = &HelmSpec{
		Chart: "ingress-nginx", ChartVersion: "4.12.1", ReleaseName: "ingress-nginx",
		TargetNamespace: "ingress-nginx", Values: json.RawMessage(`{"a":{"enabled":true},"z":1}`),
	}
	two.RequiredCapabilities = []CapabilityRequirement{{Name: "delivery.astronomer.io/scope.namespace"}, {Name: "delivery.astronomer.io/helm", Constraint: ">=2.0.0"}}
	oneDigest, err := one.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	twoDigest, err := two.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if oneDigest != twoDigest {
		t.Fatalf("canonical bundle digests differ: %s != %s", oneDigest, twoDigest)
	}

	version := BundleVersion{ID: uuid.New(), BundleID: uuid.New(), Version: 1, Spec: one, SpecDigest: oneDigest, CreatedAt: time.Now().UTC()}
	if err := version.Validate(); err != nil {
		t.Fatalf("valid bundle version: %v", err)
	}
	version.SpecDigest = digest
	if err := version.Validate(); err == nil {
		t.Fatal("mismatched bundle digest unexpectedly valid")
	}
}

func TestPlacementSafetyAndCanonicalDigest(t *testing.T) {
	t.Parallel()

	if !((Placement{}).IsEmpty()) {
		t.Fatal("zero placement must be empty")
	}
	if !(Placement{ProjectIDs: []uuid.UUID{uuid.New()}}).IsEmpty() {
		t.Fatal("project restriction must not become a positive selector")
	}
	if err := (Placement{AllClusters: true, MatchLabels: map[string]string{"env": "prod"}}).Validate(); err == nil {
		t.Fatal("all_clusters combined with labels unexpectedly valid")
	}

	a, b := uuid.New(), uuid.New()
	one := Placement{ClusterIDs: []uuid.UUID{a, b}, MatchExpressions: []LabelExpression{{Key: "region", Operator: OperatorIn, Values: []string{"west", "east"}}}}
	two := Placement{ClusterIDs: []uuid.UUID{b, a}, MatchExpressions: []LabelExpression{{Key: "region", Operator: OperatorIn, Values: []string{"east", "west"}}}}
	oneDigest, err := one.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	twoDigest, err := two.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if oneDigest != twoDigest {
		t.Fatalf("canonical placement digest differs: %s != %s", oneDigest, twoDigest)
	}
}

func TestRolloutStrategyValidationAndCanonicalDigest(t *testing.T) {
	t.Parallel()

	a, b := uuid.New(), uuid.New()
	strategy := RolloutStrategy{
		Type: StrategyCanary, MaxConcurrent: 10,
		MaxUnavailable:   Amount{Type: AmountCount, Value: 1},
		ProgressDeadline: Duration(30 * time.Minute),
		FailureThreshold: Amount{Type: AmountPercent, Value: 10},
		OnFailure:        FailureRollback,
		Canary:           &CanarySpec{ClusterIDs: []uuid.UUID{a, b}, ApprovalAfterCanary: true, Soak: Duration(time.Minute)},
	}
	one, err := strategy.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	strategy.Canary.ClusterIDs = []uuid.UUID{b, a}
	two, err := strategy.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatalf("strategy digests differ: %s != %s", one, two)
	}

	strategy.Type = StrategyRolling
	if err := strategy.Validate(); err == nil {
		t.Fatal("rolling strategy with canary settings unexpectedly valid")
	}
}

func TestCanonicalConditions(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	conditions, err := CanonicalConditions([]Condition{
		{Type: ConditionStalled, Status: ConditionFalse, ObservedGeneration: 2, LastTransitionTime: now},
		{Type: ConditionReady, Status: ConditionTrue, ObservedGeneration: 2, LastTransitionTime: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	if conditions[0].Type != ConditionReady || conditions[1].Type != ConditionStalled {
		t.Fatalf("conditions not canonical: %+v", conditions)
	}
	if _, err := CanonicalConditions(append(conditions, conditions[0])); err == nil {
		t.Fatal("duplicate condition type unexpectedly valid")
	}
}

func TestEventRejectsUnsafeCode(t *testing.T) {
	t.Parallel()

	event := Event{
		ID: uuid.New(), AggregateID: uuid.New(), Kind: EventStateTransition,
		From: "queued", To: "progressing", Code: "cohort_released", Fence: 2,
		OccurredAt: time.Now().UTC(),
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("valid event: %v", err)
	}
	event.Code = "secret\nheader: value"
	if err := event.Validate(); err == nil {
		t.Fatal("unsafe event code unexpectedly valid")
	}
}

func TestDurationUsesStringJSON(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(Duration(90 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `"1m30s"` {
		t.Fatalf("duration JSON = %s", raw)
	}
	var decoded Duration
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded != Duration(90*time.Second) {
		t.Fatalf("duration decode = %s, %v", time.Duration(decoded), err)
	}
}
