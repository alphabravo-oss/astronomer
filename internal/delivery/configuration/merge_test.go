package configuration

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestMergeDeterministicPrecedenceAndDigest(t *testing.T) {
	projectID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	clusterID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	layers := []Layer{
		{ID: clusterID, Name: "cluster", Scope: ScopeCluster, Values: json.RawMessage(`{"replicas":3,"image":{"tag":"cluster"}}`), Patches: []string{"cluster-patch"}},
		{ID: projectID, Name: "project", Scope: ScopeProject, Values: json.RawMessage(`{"image":{"tag":"project"},"feature":true}`), Patches: []string{"project-patch"}},
	}
	first, err := Merge(json.RawMessage(`{"replicas":1,"image":{"repository":"example/app","tag":"base"}}`), layers)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Merge(json.RawMessage(`{"image":{"tag":"base","repository":"example/app"},"replicas":1}`), []Layer{layers[1], layers[0]})
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Values) != `{"feature":true,"image":{"repository":"example/app","tag":"cluster"},"replicas":3}` {
		t.Fatalf("unexpected values %s", first.Values)
	}
	if first.Digest != second.Digest || string(first.Values) != string(second.Values) {
		t.Fatal("merge was not deterministic")
	}
	if !reflect.DeepEqual(first.Patches, []string{"project-patch", "cluster-patch"}) {
		t.Fatalf("unexpected patch order %#v", first.Patches)
	}
}

func TestMergeRejectsSamePrecedenceConflict(t *testing.T) {
	_, err := Merge(nil, []Layer{
		{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Scope: ScopeGroup, Precedence: 10, Values: json.RawMessage(`{"resources":{"limits":{"cpu":"1"}}}`)},
		{ID: uuid.MustParse("00000000-0000-0000-0000-000000000002"), Scope: ScopeGroup, Precedence: 10, Values: json.RawMessage(`{"resources":{"limits":{"cpu":"2"}}}`)},
	})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Path != "/resources/limits/cpu" {
		t.Fatalf("expected bounded leaf conflict, got %#v", err)
	}
}

func TestMergeNullDeletesAndArraysReplace(t *testing.T) {
	result, err := Merge(json.RawMessage(`{"remove":"me","ports":[80],"nested":{"keep":true}}`), []Layer{{
		ID: uuid.New(), Scope: ScopeRollout, Values: json.RawMessage(`{"remove":null,"ports":[443]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Values) != `{"nested":{"keep":true},"ports":[443]}` {
		t.Fatalf("unexpected values %s", result.Values)
	}
}
