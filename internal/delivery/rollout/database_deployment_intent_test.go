package rollout

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestUnconfiguredDeploymentUsesActualEffectiveRolloutResolver(t *testing.T) {
	raw := json.RawMessage(`{"kind":"helm","helm":{"chart":"scanner","chart_version":"1.0.0","release_name":"scanner","target_namespace":"scanner","values":{"image":"registry.invalid/scanner@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`)
	bundle := model.DigestBytes([]byte("actual bundle intent"))
	renderer, configuration, effective, err := resolveEffectiveRenderer(sqlc.GetDeliveryPlanningSnapshotRow{RendererSpec: raw}, bundle, model.TargetOverrides{}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := json.Marshal(renderer)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := ResolveUnconfiguredDeployment(bundle, raw)
	if err != nil || !bytes.Equal(actual.Renderer, expectedJSON) || actual.ConfigurationDigest != configuration || actual.SpecDigest != effective || actual.SpecDigest == bundle {
		t.Fatal("effective deployment data conflated bundle identity or used a different compiler")
	}
	changed := bytes.ReplaceAll(raw, []byte("registry.invalid/scanner"), []byte("registry.invalid/different"))
	other, err := ResolveUnconfiguredDeployment(bundle, changed)
	if err != nil || other.SpecDigest == actual.SpecDigest || other.ConfigurationDigest == actual.ConfigurationDigest {
		t.Fatal("changed scanner values did not change effective identity")
	}
	clear(actual.Renderer)
	again, err := ResolveUnconfiguredDeployment(bundle, raw)
	if err != nil || !bytes.Equal(again.Renderer, expectedJSON) {
		t.Fatal("caller mutated later effective renderer output")
	}
	for _, invalid := range []json.RawMessage{nil, []byte(`null`), []byte(`{"kind":"helm"}`), append(bytes.Clone(raw), '{')} {
		if _, err := ResolveUnconfiguredDeployment(bundle, invalid); err == nil {
			t.Fatal("invalid renderer compiled")
		}
	}
	if _, err := ResolveUnconfiguredDeployment("sha256:invalid", raw); err == nil {
		t.Fatal("invalid bundle digest compiled")
	}
}
