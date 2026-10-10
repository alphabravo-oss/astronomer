package delivery

import (
	"context"
	"math"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func TestInventoryNumberRejectsNonIntegralAndUnrepresentableValues(t *testing.T) {
	for _, value := range []any{float64(math.NaN()), float64(math.Inf(1)), float64(math.Inf(-1)), float64(0x1p63), float64(-1), float64(1.5), int64(-1), "9223372036854775808", "-1", "not-a-number", true} {
		if _, valid := nestedInventoryNumber(map[string]any{"count": value}, "count"); valid {
			t.Fatal("invalid number accepted")
		}
	}
	if _, valid := nestedInventoryNumber(map[string]any{"nested": "wrong-shape"}, "nested", "count"); valid {
		t.Fatal("wrong parent shape accepted")
	}
	for _, value := range []any{nil, int64(0), int32(1), float64(2), "3", int64(math.MaxInt64)} {
		if _, valid := nestedInventoryNumber(map[string]any{"count": value}, "count"); !valid {
			t.Fatal("valid optional nonnegative number rejected")
		}
	}
	if value, valid := nestedInventoryNumber(map[string]any{}, "count"); !valid || value != 0 {
		t.Fatal("optional missing field no longer zero")
	}
}

func TestInventoryCheckedSumAndNarrowing(t *testing.T) {
	total := int64(math.MaxInt64 - 1)
	if !addInventoryNumber(&total, 1) || total != math.MaxInt64 {
		t.Fatal("representable aggregate boundary changed")
	}
	if addInventoryNumber(&total, 1) || total != math.MaxInt64 {
		t.Fatal("overflow mutated aggregate")
	}
	for _, value := range []int64{-1, math.MaxInt32 + 1, 4294967298} {
		if _, valid := nestedInventoryCount32(map[string]any{"count": value}, "count"); valid {
			t.Fatal("invalid replica count narrowed")
		}
	}
	if value, valid := nestedInventoryCount32(map[string]any{"count": int64(math.MaxInt32)}, "count"); !valid || value != math.MaxInt32 {
		t.Fatal("wire maximum changed")
	}
}

func TestLegacyDynamicInvalidNumericProjectionIsNotMissing(t *testing.T) {
	dynamic := dynamicFixture()
	dynamic.PrependReactor("list", "volumes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.UnstructuredList{Items: []unstructured.Unstructured{numericLonghornVolume(int64(4294967298), int64(1024), int64(512))}}, nil
	})
	probe := &ClusterProbe{dynamic: dynamic, platformScope: true}
	component, exists := probe.inspectLonghornVolumes(context.Background())
	if !exists || component.Kind != "InventoryAvailability" || component.Health != "unavailable" || component.Observation != nil || component.HighAvailability || component.StorageReplicaCount != 0 {
		t.Fatal("legacy invalid projection missing or fabricated; observation capability layout changed")
	}
	if err := (protocol.DeliveryControllerInventory{SystemComponents: []protocol.SystemComponent{component}}).Validate(); err != nil {
		t.Fatal("legacy unavailable projection violates existing protocol")
	}
}
