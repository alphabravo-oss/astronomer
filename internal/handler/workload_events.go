package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func (h *WorkloadHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	all, names, err := h.authz.authorizedNamespaces(r.Context(), clusterID, rbac.ResourceClusters, rbac.VerbRead)
	if err != nil {
		RespondRequestError(w, r, 500, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	items := make([]map[string]any, 0)
	for _, namespace := range namespaceNames(all, names) {
		rows, err := h.listEventPages(r.Context(), clusterID.String(), namespace, queryLimitMax(r, 100, 500))
		if err != nil {
			RespondRequestError(w, r, 503, apierror.ProxyError, err.Error())
			return
		}
		items = append(items, rows...)
	}
	if !all {
		items = filterEventsByNamespace(items, names)
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, _ := items[i]["lastTimestamp"].(string)
		right, _ := items[j]["lastTimestamp"].(string)
		if left != right {
			return left > right
		}
		return items[i]["id"].(string) < items[j]["id"].(string)
	})
	page, metadata := pageWindow(r, items)
	paging.Write(w, page, metadata)
}

func (h *WorkloadHandler) listEventPages(ctx context.Context, clusterID, namespace string, limit int) ([]map[string]any, error) {
	path := "/api/v1/events"
	if namespace != "" {
		path = "/api/v1/namespaces/" + namespace + "/events"
	}
	query := url.Values{"limit": []string{strconv.Itoa(limit)}}
	var items []map[string]any
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var page eventList
		if err := h.getJSON(ctx, clusterID, path+"?"+query.Encode(), &page); err != nil {
			return nil, err
		}
		for _, evt := range page.Items {
			items = append(items, map[string]any{"id": evt.Metadata.UID, "type": evt.Type, "reason": evt.Reason, "message": evt.Message,
				"involvedObject": map[string]any{"kind": evt.InvolvedObject.Kind, "name": evt.InvolvedObject.Name, "namespace": evt.InvolvedObject.Namespace},
				"count":          evt.Count, "firstTimestamp": evt.FirstTimestamp, "lastTimestamp": evt.LastTimestamp})
		}
		next := page.Metadata.Continue
		if next == "" {
			return items, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("event pagination continuation did not advance")
		}
		seen[next] = true
		query.Set("continue", next)
	}
}
