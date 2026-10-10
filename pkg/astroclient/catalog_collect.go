package astroclient

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type CatalogChartPageClient interface {
	GetCatalogChartsWithResponse(context.Context, *GetCatalogChartsParams, ...RequestEditorFn) (*GetCatalogChartsResponse, error)
}

// CollectCatalogCharts follows signed continuations even through empty windows.
// It returns no partial result on cancellation/revocation/invalid progression or
// explicit traversal bounds. The caller supplies a deadline; this is not a
// consistent database snapshot across requests or a bulk-selection authority.
func CollectCatalogCharts(ctx context.Context, client CatalogChartPageClient, params GetCatalogChartsParams) ([]HelmChart, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("catalog traversal requires a deadline")
	}
	if client == nil {
		return nil, errors.New("catalog traversal requires a client")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if params.Offset != nil {
		return nil, errors.New("catalog traversal cannot combine offset and cursor")
	}
	cursor := ""
	seenTokens := map[string]bool{}
	seenCharts := map[uuid.UUID]bool{}
	out := []HelmChart{}
	for page := 0; page < 10000; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := client.GetCatalogChartsWithResponse(ctx, &params, WithPaginationCursor(cursor))
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if response == nil || response.HTTPResponse == nil || response.StatusCode() != http.StatusOK || response.JSON200 == nil {
			return nil, errors.New("catalog traversal did not return an authorized page")
		}
		data := response.JSON200.Data
		metadata := response.JSON200.Pagination
		if metadata.Limit < 1 || metadata.Limit > 200 || len(data) > metadata.Limit || metadata.Offset != len(out) || len(out)+len(data) > 100000 {
			return nil, errors.New("catalog traversal page exceeded its contract")
		}
		for _, chart := range data {
			if chart.Id == nil || *chart.Id == uuid.Nil || seenCharts[*chart.Id] {
				return nil, errors.New("catalog traversal chart identity repeated or missing")
			}
			seenCharts[*chart.Id] = true
		}
		out = append(out, data...)
		if !metadata.HasMore {
			if metadata.NextCursor != nil && *metadata.NextCursor != "" {
				return nil, errors.New("terminal catalog page carried continuation")
			}
			return out, nil
		}
		if metadata.NextCursor == nil || *metadata.NextCursor == "" || len(*metadata.NextCursor) > 4096 || seenTokens[*metadata.NextCursor] {
			return nil, errors.New("catalog traversal did not advance")
		}
		cursor = *metadata.NextCursor
		seenTokens[cursor] = true
	}
	return nil, errors.New("catalog traversal exceeds the 10000-window diagnostic limit")
}
