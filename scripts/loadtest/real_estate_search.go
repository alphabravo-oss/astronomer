package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type estateScopeCheck struct {
	At time.Time `json:"at"`
	OK bool      `json:"ok"`
}

// This is snapshot proof, not an atomic membership fence around each search.
// Restricted credentials cannot equate clusters:list with resource-type:list.
func verifyEstateSearch(ctx context.Context, client *http.Client, base, token string, scope *estateSearchSpec) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var permissions struct {
		Subject struct {
			Self bool `json:"self"`
		} `json:"subject"`
		Superuser bool `json:"superuser"`
	}
	if err := estateGET(ctx, client, base+"/api/v1/rbac/my-permissions", token, &permissions, true); err != nil {
		return errors.New("search permission proof unavailable")
	}
	if !permissions.Subject.Self || !permissions.Superuser {
		return errors.New("search requires verified self superuser binding")
	}
	expected := map[string]bool{}
	for _, id := range scope.Fanout {
		expected[id] = true
	}
	seen := map[string]bool{}
	// Bounded offset pagination is explicit; reject inconsistent total/pages.
	for offset := 0; offset <= 32; offset += 20 {
		var page struct {
			Data []struct {
				ID      string     `json:"id"`
				Status  string     `json:"status"`
				Deleted *time.Time `json:"decommissioned_at"`
			} `json:"data"`
			Pagination *struct {
				Total   *int `json:"total"`
				HasMore bool `json:"has_more"`
			} `json:"pagination"`
		}
		path := fmt.Sprintf("%s/api/v1/clusters/?status=active&limit=20&offset=%d", base, offset)
		if err := estateGET(ctx, client, path, token, &page, false); err != nil {
			return errors.New("search active-cluster census unavailable")
		}
		if page.Pagination == nil || page.Pagination.Total == nil || *page.Pagination.Total != len(expected) || len(page.Data) == 0 || len(page.Data) > 20 {
			return errors.New("search active-cluster census differs")
		}
		for _, cluster := range page.Data {
			if !expected[cluster.ID] || seen[cluster.ID] || cluster.Status != "active" || cluster.Deleted != nil {
				return errors.New("unexpected search fanout member")
			}
			seen[cluster.ID] = true
		}
		if !page.Pagination.HasMore {
			if len(seen) != len(expected) {
				return errors.New("incomplete search fanout census")
			}
			return nil
		}
		if len(page.Data) != 20 || len(seen) >= len(expected) {
			return errors.New("invalid search pagination")
		}
	}
	return errors.New("search census exceeds bound")
}
func validateEstateSearchBody(raw []byte, scope *estateSearchSpec, kind string) error {
	var response struct {
		Data *struct {
			Type      string `json:"type"`
			Queried   *int   `json:"clusters_queried"`
			Failed    *int   `json:"clusters_failed"`
			Truncated *bool  `json:"truncated"`
			Results   []struct {
				ClusterID string `json:"cluster_id"`
				Namespace string `json:"namespace"`
			} `json:"results"`
			Errors []json.RawMessage `json:"errors"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Data == nil {
		return errors.New("invalid_search_body")
	}
	d := response.Data
	if d.Type != kind || d.Queried == nil || *d.Queried != len(scope.Fanout) || d.Failed == nil || *d.Failed != 0 || d.Truncated == nil || d.Results == nil || d.Errors == nil || len(d.Errors) > 0 {
		return errors.New("incomplete_search")
	}
	if *d.Truncated {
		return errors.New("truncated_search")
	}
	expected := map[string]bool{}
	for _, id := range scope.Fanout {
		expected[id] = true
	}
	if len(d.Results) > scope.Limit {
		return errors.New("invalid_search_limit")
	}
	for _, item := range d.Results {
		if !expected[item.ClusterID] || item.Namespace != scope.Namespace {
			return errors.New("foreign_search_result")
		}
	}
	return nil
}
