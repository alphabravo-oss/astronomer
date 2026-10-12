package handler

import (
	"encoding/json"
	"errors"
	"math"
)

var errGrafanaReplicaMetadata = errors.New("stored Grafana replica metadata is not a representable integer")

// grafanaMetadataReplicas preserves the existing int32 metadata domain. Missing
// values retain the old zero value; present values must not narrow or truncate.
func grafanaMetadataReplicas(value any) (int32, error) {
	switch number := value.(type) {
	case nil:
		return 0, nil
	case int32:
		return number, nil
	case int:
		if number < math.MinInt32 || number > math.MaxInt32 {
			return 0, errGrafanaReplicaMetadata
		}
		return int32(number), nil
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < math.MinInt32 || number > math.MaxInt32 {
			return 0, errGrafanaReplicaMetadata
		}
		return int32(number), nil
	case json.Number:
		integer, err := number.Int64()
		if err != nil || integer < math.MinInt32 || integer > math.MaxInt32 {
			return 0, errGrafanaReplicaMetadata
		}
		return int32(integer), nil
	default:
		return 0, errGrafanaReplicaMetadata
	}
}
