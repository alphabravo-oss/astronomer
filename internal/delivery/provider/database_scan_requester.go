package provider

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Created privately from exact locked claim/canonical typed object requests.
// A machine origin is attribution, not a permission to forward arbitrary calls.
// This wrapper can only reach the reviewed finite paths and content hashes;
// writes carry no user headers, registry tokens or arbitrary identity hints.
type databaseScanScopedRequester struct {
	requester databaseScanRequester
	cluster   uuid.UUID
	reads     map[string]bool
	writes    map[string]string
}

func (scope *databaseScanScopedRequester) Do(ctx context.Context, cluster, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	if ctx == nil || scope == nil || scope.requester == nil || scope.cluster == uuid.Nil || cluster != scope.cluster.String() {
		return nil, errors.New("owned database scan request cluster differs")
	}
	identity := callerid.Resolve(ctx)
	source, marked := callerid.MachineSourceFromContext(ctx)
	if callerid.HasAuthenticatedUserContext(ctx) || !marked || source != callerid.SourceDatabaseScan || !identity.IsMachine() || identity.User != callerid.MachineSubject(callerid.SourceDatabaseScan) {
		return nil, errors.New("owned database scan request origin invalid")
	}
	switch method {
	case http.MethodGet:
		if !scope.reads[path] || len(body) != 0 || len(headers) != 0 {
			return nil, errors.New("owned database scan read outside current claim")
		}
	case http.MethodPost:
		expected, found := scope.writes[path]
		if !found || len(body) == 0 || len(body) > databaseScanMaximumAPIBytes || digest.FromBytes(body).String() != expected || !reflect.DeepEqual(headers, map[string]string{"Content-Type": "application/json"}) {
			return nil, errors.New("owned database scan write outside exact owned object")
		}
	default:
		return nil, errors.New("owned database scan request verb unsupported")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return scope.requester.Do(ctx, cluster, method, path, body, headers)
}

// These maps are private immutable snapshots; the caller cannot mutate an
// already-composed requester's scope through reused constructor slices/maps.
func newDatabaseScanScopedRequester(requester databaseScanRequester, cluster uuid.UUID, reads []string, writes map[string]string) (*databaseScanScopedRequester, error) {
	if requester == nil || cluster == uuid.Nil || len(reads) > 32 || len(writes) > 6 {
		return nil, errors.New("owned database scan request scope unavailable")
	}
	scope := &databaseScanScopedRequester{requester: requester, cluster: cluster, reads: map[string]bool{}, writes: map[string]string{}}
	for _, path := range reads {
		if !databaseScanOwnedReadPath(path) {
			return nil, errors.New("owned database scan request read shape invalid")
		}
		if scope.reads[path] {
			return nil, errors.New("owned database scan duplicate read scope")
		}
		scope.reads[path] = true
	}
	for path, hash := range writes {
		if !databaseScanOwnedWriteCollection(path) || !validDatabaseScanDigest(hash) {
			return nil, errors.New("owned database scan request write shape invalid")
		}
		scope.writes[path] = hash
	}
	return scope, nil
}

func validDatabaseScanDigest(value string) bool {
	selected := digest.Digest(value)
	return selected.Validate() == nil && selected.Algorithm() == digest.SHA256
}
func databaseScanOwnedWriteCollection(path string) bool {
	switch path {
	case "/api/v1/namespaces", "/api/v1/namespaces/astronomer-database-scans/secrets", "/api/v1/namespaces/astronomer-database-scans/persistentvolumeclaims", "/apis/batch/v1/namespaces/astronomer-database-scans/jobs", "/apis/networking.k8s.io/v1/namespaces/astronomer-database-scans/networkpolicies":
		return true
	default:
		return false
	}
}
func databaseScanOwnedReadPath(path string) bool {
	parsed, err := url.ParseRequestURI(path)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Fragment != "" || parsed.User != nil || parsed.EscapedPath() != parsed.Path {
		return false
	}
	parts := strings.Split(parsed.Path, "/")
	validName := func(value string) bool { return value != "" && len(validation.IsDNS1123Subdomain(value)) == 0 }
	if parsed.Path == "/api/v1/pods" {
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil || len(query) < 1 || len(query) > 2 || len(query["limit"]) != 1 || query.Get("limit") != "64" {
			return false
		}
		token := query.Get("continue")
		if len(query["continue"]) > 1 || len(token) > 4096 {
			return false
		}
		expected := "limit=64"
		if token != "" {
			expected += "&continue=" + url.QueryEscape(token)
		}
		return parsed.RawQuery == expected
	}
	if parsed.Path == "/api/v1/namespaces/astronomer-database-scans" {
		return parsed.RawQuery == ""
	}
	if len(parts) == 5 && parts[1] == "api" && parts[2] == "v1" && parts[3] == "nodes" {
		return validName(parts[4]) && parsed.RawQuery == ""
	}
	if len(parts) >= 7 && parts[1] == "api" && parts[2] == "v1" && parts[3] == "namespaces" && validName(parts[4]) {
		if parts[5] == "pods" && len(parts) == 7 && validName(parts[6]) {
			return parsed.RawQuery == ""
		}
		if parts[4] != "astronomer-database-scans" {
			return false
		}
		if parts[5] == "pods" && len(parts) == 8 && validName(parts[6]) && parts[7] == "log" {
			for _, query := range []string{"container=observe&timestamps=true&limitBytes=33025", "container=scanner&timestamps=false&limitBytes=8388609", "container=prepare&timestamps=true&limitBytes=16513"} {
				if parsed.RawQuery == query {
					return true
				}
			}
			return false
		}
		if len(parts) == 7 && (parts[5] == "secrets" || parts[5] == "persistentvolumeclaims") {
			return validName(parts[6]) && parsed.RawQuery == ""
		}
	}
	if len(parts) == 6 && parts[1] == "api" && parts[2] == "v1" && parts[3] == "namespaces" && parts[4] == "astronomer-database-scans" && parts[5] == "pods" {
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil || len(query) != 2 || len(query["limit"]) != 1 || query.Get("limit") != "2" || len(query["labelSelector"]) != 1 {
			return false
		}
		uid, found := strings.CutPrefix(query.Get("labelSelector"), "batch.kubernetes.io/controller-uid=")
		return found && validDatabaseScanUID(types.UID(uid)) && parsed.RawQuery == "labelSelector="+url.QueryEscape(query.Get("labelSelector"))+"&limit=2"
	}
	if len(parts) == 8 && parts[1] == "apis" && (parts[2] == "batch" && parts[3] == "v1" || parts[2] == "networking.k8s.io" && parts[3] == "v1") && parts[4] == "namespaces" && parts[5] == "astronomer-database-scans" && validName(parts[7]) {
		return (parts[2] == "batch" && parts[6] == "jobs" || parts[2] == "networking.k8s.io" && parts[6] == "networkpolicies") && parsed.RawQuery == ""
	}
	if parsed.Path == "/apis/networking.k8s.io/v1/namespaces/astronomer-database-scans/networkpolicies" {
		return parsed.RawQuery == "limit=65"
	}
	return false
}
