package delivery

import (
	"math"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Dynamic CRDs may encode byte quantities as decimal strings. Preserve absent
// optional fields as zero, but never turn malformed or overflowing values into
// usable storage or replica observations.
func nestedInventoryNumber(object map[string]any, fields ...string) (int64, bool) {
	value, found, err := unstructured.NestedFieldNoCopy(object, fields...)
	if err != nil {
		return 0, false
	}
	if !found || value == nil {
		return 0, true
	}
	var number int64
	switch typed := value.(type) {
	case int64:
		number = typed
	case int32:
		number = int64(typed)
	case float64:
		// 2^63 is exactly representable; float64(MaxInt64) rounds up to it.
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 || typed >= 0x1p63 || math.Trunc(typed) != typed {
			return 0, false
		}
		number = int64(typed)
	case string:
		var parseErr error
		number, parseErr = strconv.ParseInt(typed, 10, 64)
		if parseErr != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return number, number >= 0
}

func nestedInventoryCount32(object map[string]any, fields ...string) (int32, bool) {
	value, valid := nestedInventoryNumber(object, fields...)
	if !valid || value > math.MaxInt32 {
		return 0, false
	}
	return int32(value), true
}

func inventoryLength32(value int) (int32, bool) {
	if value < 0 || value > math.MaxInt32 {
		return 0, false
	}
	return int32(value), true
}

func addInventoryNumber(total *int64, value int64) bool {
	if *total < 0 || value < 0 || value > math.MaxInt64-*total {
		return false
	}
	*total += value
	return true
}

func addNestedInventoryNumber(total *int64, object map[string]any, fields ...string) bool {
	value, valid := nestedInventoryNumber(object, fields...)
	return valid && addInventoryNumber(total, value)
}

func invalidInventoryProjection(kind, name string) protocol.SystemComponent {
	return protocol.SystemComponent{
		ID: "cluster/observation/" + kind, Name: name, Category: "observation", Owner: "cluster",
		ManagementMethod: "observation", Kind: "InventoryAvailability", Health: "unavailable",
		Detail: "Inventory numeric observation is invalid or exceeds its wire range; this is not proof that resources are missing.",
	}
}
