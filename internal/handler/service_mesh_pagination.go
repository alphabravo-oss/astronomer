package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

const meshListTimeout = 20 * time.Second
const meshListMaxBytes = 2 << 20

var errMeshResourceAbsent = errors.New("mesh resource absent")

// The shared Kubernetes collector owns the 500-item/10-page/5000-item bounds,
// opaque continuation and response-only snapshot identity. No partial items
// escape on expiry, a repeated token, a changed snapshot or a stopped request.
func collectMeshPages[T any](ctx context.Context, requester K8sRequester, clusterID uuid.UUID, path string) ([]T, error) {
	absent := false
	items, err := collectSupportPages(ctx, func(ctx context.Context, options metav1.ListOptions) ([]T, metav1.ListMeta, error) {
		query := url.Values{"limit": {strconv.FormatInt(options.Limit, 10)}}
		if options.Continue != "" {
			query.Set("continue", options.Continue)
		}
		resp, err := requester.Do(ctx, clusterID.String(), http.MethodGet, path+"?"+query.Encode(), nil, requestHeaders(""))
		if err != nil || resp == nil {
			return nil, metav1.ListMeta{}, errors.New("mesh list unavailable")
		}
		if resp.StatusCode == http.StatusNotFound && options.Continue == "" {
			absent = true
			return nil, metav1.ListMeta{}, errMeshResourceAbsent
		}
		if resp.StatusCode == http.StatusGone {
			return nil, metav1.ListMeta{}, apierrors.NewResourceExpired("mesh continuation expired")
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, metav1.ListMeta{}, errors.New("mesh list status failed")
		}
		// Reject oversized encoded bodies before allocating their decoded copy.
		if len(resp.Body) > ((meshListMaxBytes+2)/3)*4 {
			return nil, metav1.ListMeta{}, errors.New("mesh list body budget exceeded")
		}
		body, err := decodeResponseBody(resp)
		if err != nil || len(body) > meshListMaxBytes {
			return nil, metav1.ListMeta{}, errors.New("mesh list body invalid")
		}
		// Kubernetes CRDs are additive supplier shapes. The canonical any reader
		// rejects duplicate keys/depth/trailing data before the typed projection.
		var document any
		if err := jsoncontract.Decode(body, &document); err != nil {
			return nil, metav1.ListMeta{}, errors.New("mesh list JSON invalid")
		}
		var envelope struct {
			Metadata metav1.ListMeta `json:"metadata"`
			Items    []T             `json:"items"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil || envelope.Items == nil {
			return nil, metav1.ListMeta{}, errors.New("mesh list items invalid")
		}
		return envelope.Items, envelope.Metadata, nil
	})
	if absent && ctx.Err() == nil {
		return nil, errMeshResourceAbsent
	}
	return items, err
}
