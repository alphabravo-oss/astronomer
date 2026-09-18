package server

import (
	"bytes"
	"errors"
	"io"
	"testing"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

func TestLocalFluxBootstrapSupportsEveryPinnedDistributionObject(t *testing.T) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(fluxdistribution.InstallYAML()), 64<<10)
	objects := 0
	for {
		var object unstructured.Unstructured
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode distribution: %v", err)
		}
		if object.GetKind() == "" {
			continue
		}
		objects++
		if _, ok := localFluxResources[object.GroupVersionKind()]; !ok {
			t.Fatalf("unsupported bootstrap object %s %s/%s", object.GroupVersionKind(), object.GetNamespace(), object.GetName())
		}
	}
	if objects == 0 {
		t.Fatal("embedded Flux distribution is empty")
	}
}
