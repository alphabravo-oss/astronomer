package handler

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestGrafanaMetadataReplicasPreservesRepresentableValues(t *testing.T) {
	for _, item := range []struct {
		name  string
		input any
		want  int32
	}{
		{"missing", nil, 0}, {"int32-zero", int32(0), 0}, {"int-zero", 0, 0}, {"negative-existing", int32(-1), -1},
		{"int-min", int(math.MinInt32), math.MinInt32}, {"int-max", int(math.MaxInt32), math.MaxInt32},
		{"float-min", float64(math.MinInt32), math.MinInt32}, {"float-max", float64(math.MaxInt32), math.MaxInt32},
		{"float-one", float64(1), 1}, {"number-min", json.Number("-2147483648"), math.MinInt32},
		{"number-max", json.Number("2147483647"), math.MaxInt32},
	} {
		t.Run(item.name, func(t *testing.T) {
			got, err := grafanaMetadataReplicas(item.input)
			if err != nil || got != item.want {
				t.Fatalf("got %d/error=%t; want %d", got, err != nil, item.want)
			}
		})
	}
}

func TestGrafanaMetadataReplicasRejectsNarrowingBeforeConversion(t *testing.T) {
	for _, item := range []struct {
		name  string
		input any
	}{
		{"int-high", int(math.MaxInt32) + 1}, {"int-low", int(math.MinInt32) - 1},
		{"float-high", float64(math.MaxInt32) + 1}, {"float-low", float64(math.MinInt32) - 1},
		{"fraction", float64(1.25)}, {"nan", math.NaN()}, {"infinity", math.Inf(1)},
		{"number-high", json.Number("2147483648")}, {"number-low", json.Number("-2147483649")},
		{"number-fraction", json.Number("1.25")}, {"number-unparseable", json.Number("not-a-number")},
		{"wrong-type", "1"},
	} {
		t.Run(item.name, func(t *testing.T) {
			got, err := grafanaMetadataReplicas(item.input)
			if got != 0 || !errors.Is(err, errGrafanaReplicaMetadata) {
				t.Fatal("invalid metadata was converted")
			}
		})
	}
}
