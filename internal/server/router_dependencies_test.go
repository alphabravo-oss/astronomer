package server

import (
	"reflect"
	"testing"
)

func TestRouterDependenciesRemainDomainGrouped(t *testing.T) {
	t.Parallel()

	type groupContract struct {
		name   string
		typeOf reflect.Type
	}
	want := []groupContract{
		{name: "CoreAuth", typeOf: reflect.TypeOf(CoreAuthDependencies{})},
		{name: "ClusterResources", typeOf: reflect.TypeOf(ClusterResourceDependencies{})},
		{name: "Delivery", typeOf: reflect.TypeOf(DeliveryDependencies{})},
		{name: "AdminPlatform", typeOf: reflect.TypeOf(AdminPlatformDependencies{})},
		{name: "StreamingInternal", typeOf: reflect.TypeOf(StreamingInternalDependencies{})},
	}

	typ := reflect.TypeOf(RouterDependencies{})
	if typ.NumField() != len(want) {
		t.Fatalf("RouterDependencies has %d fields, want exactly %d domain groups", typ.NumField(), len(want))
	}
	for i, contract := range want {
		field := typ.Field(i)
		if field.Name != contract.name || field.Type != contract.typeOf {
			t.Errorf("field %d = %s %v, want %s %v", i, field.Name, field.Type, contract.name, contract.typeOf)
		}
		if field.Anonymous {
			t.Errorf("field %s must be explicit; anonymous embedding flattens the dependency boundary", field.Name)
		}
	}
}
