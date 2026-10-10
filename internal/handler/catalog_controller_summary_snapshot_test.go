package handler

import (
	"context"
	"testing"
)

func TestCatalogControllerSummarySeparateReadsNeverProveSnapshot(t *testing.T) {
	for _, count := range []int64{0, 1} {
		q := &catalogControllerSummaryQuerier{minimalCatalogQuerier: &minimalCatalogQuerier{}, countOverride: &count}
		summary, err := NewCatalogHandler(q).controllerSummary(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		window := summary["operationSummary"].(map[string]any)
		if window["complete"] != false || window["total"] != count || window["returned"] != 0 {
			t.Fatal("independent list/count equality claimed a complete snapshot or altered the observed count")
		}
	}
}
