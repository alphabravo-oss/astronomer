package grafanaproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	promClusterLabel = "cluster_id"
	lokiClusterLabel = "cluster"
)

// rewriteTenantQuery enforces cluster_id (PromQL) / cluster (LogQL) on Grafana 11
// query paths for cluster-scoped users. Superuser and global monitoring:update
// callers have empty ClusterIDs and never reach this.
func rewriteTenantQuery(r *http.Request, clusterIDs []string) (*http.Request, error) {
	ids := normalizeClusterIDs(clusterIDs)
	if len(ids) == 0 {
		return r, nil
	}
	path := strings.ToLower(r.URL.Path)
	kind := queryPathKind(path)
	if kind == queryKindNone {
		return r, nil
	}

	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		q, err := rewriteQueryValues(r.URL.Query(), kind, ids)
		if err != nil {
			return nil, err
		}
		cloned := r.Clone(r.Context())
		cloned.URL = r.URL.ResolveReference(&url.URL{RawQuery: q.Encode()})
		return cloned, nil
	}

	body, err := readRewriteBody(r)
	if err != nil {
		return nil, err
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	switch {
	case kind == queryKindDS && jsonLooksLikeObject(body):
		body, err = rewriteDSQueryBody(body, ids)
	case strings.Contains(ct, "application/json") && jsonLooksLikeObject(body):
		body, err = rewriteJSONQueryBody(body, kind, ids)
	case strings.Contains(ct, "application/x-www-form-urlencoded") || (len(body) > 0 && !jsonLooksLikeObject(body)):
		body, err = rewriteFormBody(body, r.URL.Query(), kind, ids)
	case len(body) == 0:
		q, qerr := rewriteQueryValues(r.URL.Query(), kind, ids)
		if qerr != nil {
			return nil, qerr
		}
		cloned := r.Clone(r.Context())
		cloned.URL = r.URL.ResolveReference(&url.URL{RawQuery: q.Encode()})
		cloned.Body = http.NoBody
		cloned.ContentLength = 0
		return cloned, nil
	default:
		if kind == queryKindDS {
			body, err = rewriteDSQueryBody(body, ids)
		} else {
			body, err = rewriteJSONQueryBody(body, kind, ids)
		}
	}
	if err != nil {
		return nil, err
	}
	cloned := r.Clone(r.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	cloned.ContentLength = int64(len(body))
	cloned.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	cloned.Header = r.Header.Clone()
	cloned.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return cloned, nil
}

type queryKind int

const (
	queryKindNone queryKind = iota
	queryKindDS
	queryKindProm
	queryKindLoki
)

func queryPathKind(path string) queryKind {
	p := strings.ToLower(strings.TrimSuffix(path, "/"))
	if p == "/api/ds/query" || p == "/api/tsdb/query" {
		return queryKindDS
	}
	if !isGrafanaDatasourceQueryPath(p) {
		return queryKindNone
	}
	if strings.Contains(p, "/loki/") || datasourceUIDIs(p, "loki") {
		return queryKindLoki
	}
	if isPromQuerySuffix(p) || datasourceUIDIs(p, "thanos") || datasourceUIDIs(p, "prometheus") {
		return queryKindProm
	}
	if isLokiQuerySuffix(p) {
		return queryKindLoki
	}
	return queryKindNone
}

func isGrafanaDatasourceQueryPath(p string) bool {
	if strings.HasPrefix(p, "/api/datasources/proxy/") {
		return true
	}
	if strings.Contains(p, "/resources/") || strings.Contains(p, "/proxy/") {
		return strings.HasPrefix(p, "/api/datasources/")
	}
	return false
}

func datasourceUIDIs(path, uid string) bool {
	uid = strings.ToLower(uid)
	markers := []string{
		"/api/datasources/uid/" + uid + "/",
		"/api/datasources/proxy/uid/" + uid + "/",
	}
	for _, m := range markers {
		if strings.Contains(path, m) {
			return true
		}
	}
	return false
}

func isPromQuerySuffix(path string) bool {
	return strings.Contains(path, "/api/v1/query") ||
		strings.Contains(path, "/api/v1/series") ||
		strings.Contains(path, "/api/v1/labels") ||
		strings.Contains(path, "/api/v1/label/") ||
		strings.Contains(path, "/api/v1/query_exemplars") ||
		strings.Contains(path, "/api/v1/format_query")
}

func isLokiQuerySuffix(path string) bool {
	return strings.Contains(path, "/loki/api/v1/query") ||
		strings.Contains(path, "/loki/api/v1/series") ||
		strings.Contains(path, "/loki/api/v1/labels") ||
		strings.Contains(path, "/loki/api/v1/label/") ||
		strings.Contains(path, "/loki/api/v1/tail") ||
		strings.Contains(path, "/loki/api/v1/index/stats") ||
		strings.Contains(path, "/loki/api/v1/index/volume") ||
		strings.Contains(path, "/loki/api/v1/patterns") ||
		strings.Contains(path, "/loki/api/v1/detected_")
}

func readRewriteBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func jsonLooksLikeObject(body []byte) bool {
	trim := bytes.TrimSpace(body)
	return len(trim) > 0 && trim[0] == '{'
}

func rewriteDSQueryBody(body []byte, clusterIDs []string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("ds/query: %w", err)
	}
	queries, _ := payload["queries"].([]any)
	for i, raw := range queries {
		qm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if err := rewriteQueryObject(qm, clusterIDs); err != nil {
			return nil, err
		}
		queries[i] = qm
	}
	payload["queries"] = queries
	return json.Marshal(payload)
}

func rewriteJSONQueryBody(body []byte, kind queryKind, clusterIDs []string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("query json: %w", err)
	}
	if err := rewriteQueryMapFields(payload, kind, clusterIDs); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

func rewriteQueryObject(qm map[string]any, clusterIDs []string) error {
	kind := kindFromDatasource(qm)
	if kind == queryKindNone {
		return nil
	}
	if err := rewriteQueryMapFields(qm, kind, clusterIDs); err != nil {
		return err
	}
	if extra, ok := qm["extraFilters"].(string); ok && strings.TrimSpace(extra) != "" && kind == queryKindLoki {
		rewritten, err := injectMatcherList(extra, lokiClusterLabel, clusterIDs)
		if err != nil {
			return err
		}
		qm["extraFilters"] = rewritten
	}
	return nil
}

func rewriteQueryMapFields(qm map[string]any, kind queryKind, clusterIDs []string) error {
	for _, key := range []string{"expr", "query"} {
		s, ok := qm[key].(string)
		if !ok || strings.TrimSpace(s) == "" {
			continue
		}
		rewritten, err := rewriteExpr(s, kind, clusterIDs)
		if err != nil {
			return err
		}
		qm[key] = rewritten
	}
	for _, nestedKey := range []string{"instantQuery", "rangeQuery"} {
		switch nested := qm[nestedKey].(type) {
		case string:
			if strings.TrimSpace(nested) == "" {
				continue
			}
			rewritten, err := rewriteExpr(nested, kind, clusterIDs)
			if err != nil {
				return err
			}
			qm[nestedKey] = rewritten
		case map[string]any:
			if err := rewriteQueryMapFields(nested, kind, clusterIDs); err != nil {
				return err
			}
		}
	}
	return nil
}

func kindFromDatasource(qm map[string]any) queryKind {
	switch ds := qm["datasource"].(type) {
	case string:
		return kindFromUID(ds)
	case map[string]any:
		if t, ok := ds["type"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(t)) {
			case "loki":
				return queryKindLoki
			case "prometheus", "thanos":
				return queryKindProm
			}
		}
		if uid, ok := ds["uid"].(string); ok {
			return kindFromUID(uid)
		}
	}
	return queryKindNone
}

func kindFromUID(uid string) queryKind {
	u := strings.ToLower(strings.TrimSpace(uid))
	switch {
	case u == "loki" || strings.Contains(u, "loki"):
		return queryKindLoki
	case u == "thanos" || u == "prometheus" || strings.Contains(u, "thanos") || strings.Contains(u, "prom"):
		return queryKindProm
	default:
		return queryKindNone
	}
}

func rewriteExpr(expr string, kind queryKind, clusterIDs []string) (string, error) {
	switch kind {
	case queryKindLoki:
		return injectLogQLMatcher(expr, lokiClusterLabel, clusterIDs)
	default:
		return injectPromQLMatcher(expr, promClusterLabel, clusterIDs)
	}
}

func rewriteQueryValues(q url.Values, kind queryKind, clusterIDs []string) (url.Values, error) {
	out := cloneValues(q)
	rewrote := false
	for _, key := range []string{"query", "match[]", "match"} {
		vals := out[key]
		if len(vals) == 0 {
			continue
		}
		for i, v := range vals {
			if strings.TrimSpace(v) == "" {
				continue
			}
			rewritten, err := rewriteExpr(v, kind, clusterIDs)
			if err != nil {
				return nil, err
			}
			vals[i] = rewritten
			rewrote = true
		}
		out[key] = vals
	}
	if !rewrote && needsMatchSelector(kind) {
		selector := forcedSelector(kind, clusterIDs)
		out.Add("match[]", selector)
	}
	return out, nil
}

func rewriteFormBody(body []byte, query url.Values, kind queryKind, clusterIDs []string) ([]byte, error) {
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, fmt.Errorf("form query: %w", err)
	}
	merged := cloneValues(query)
	for k, vs := range vals {
		merged[k] = append([]string(nil), vs...)
	}
	rewritten, err := rewriteQueryValues(merged, kind, clusterIDs)
	if err != nil {
		return nil, err
	}
	// Keep body keys in the body; path query is not duplicated here.
	form := url.Values{}
	for k, vs := range vals {
		if rw, ok := rewritten[k]; ok {
			form[k] = rw
		} else {
			form[k] = vs
		}
	}
	if len(vals) == 0 {
		return []byte(rewritten.Encode()), nil
	}
	if _, hadMatch := vals["match[]"]; !hadMatch {
		if added := rewritten["match[]"]; len(added) > 0 && needsMatchSelector(kind) {
			form["match[]"] = added
		}
	}
	return []byte(form.Encode()), nil
}

func needsMatchSelector(kind queryKind) bool {
	return kind == queryKindProm || kind == queryKindLoki
}

func forcedSelector(kind queryKind, clusterIDs []string) string {
	label := promClusterLabel
	if kind == queryKindLoki {
		label = lokiClusterLabel
	}
	inner, _ := injectMatcherList("", label, clusterIDs)
	return "{" + inner + "}"
}

func cloneValues(in url.Values) url.Values {
	out := make(url.Values, len(in))
	for k, vs := range in {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func normalizeClusterIDs(ids []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func matcherForClusters(_ string, clusterIDs []string) (op, value string) {
	ids := normalizeClusterIDs(clusterIDs)
	if len(ids) == 1 {
		return "=", strconv.Quote(ids[0])
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = regexp.QuoteMeta(id)
	}
	return "=~", strconv.Quote(strings.Join(parts, "|"))
}

type labelMatcher struct {
	name string
	op   string
	val  string // quoted literal
}

func injectMatcherList(inner, label string, clusterIDs []string) (string, error) {
	matchers, err := parseMatcherList(inner)
	if err != nil {
		return "", err
	}
	filtered := matchers[:0]
	for _, m := range matchers {
		if m.name == label {
			continue
		}
		filtered = append(filtered, m)
	}
	op, val := matcherForClusters(label, clusterIDs)
	filtered = append(filtered, labelMatcher{name: label, op: op, val: val})
	parts := make([]string, len(filtered))
	for i, m := range filtered {
		parts[i] = m.name + m.op + m.val
	}
	return strings.Join(parts, ","), nil
}

func parseMatcherList(inner string) ([]labelMatcher, error) {
	s := strings.TrimSpace(inner)
	if s == "" {
		return nil, nil
	}
	var out []labelMatcher
	i := 0
	for i < len(s) {
		i = skipSpace(s, i)
		if i >= len(s) {
			break
		}
		if s[i] == ',' {
			i++
			continue
		}
		name, next, ok := readIdent(s, i)
		if !ok {
			return nil, fmt.Errorf("matcher list: expected label at %d", i)
		}
		i = skipSpace(s, next)
		op, next, ok := readMatcherOp(s, i)
		if !ok {
			return nil, fmt.Errorf("matcher list: expected operator after %s", name)
		}
		i = skipSpace(s, next)
		val, next, err := readQuoted(s, i)
		if err != nil {
			return nil, err
		}
		out = append(out, labelMatcher{name: name, op: op, val: val})
		i = skipSpace(s, next)
		if i < len(s) && s[i] == ',' {
			i++
		}
	}
	return out, nil
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

func readIdent(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", i, false
	}
	if !isIdentStart(s[i]) {
		return "", i, false
	}
	j := i + 1
	for j < len(s) && isIdentCont(s[j]) {
		j++
	}
	return s[i:j], j, true
}

func isIdentStart(b byte) bool {
	return b == '_' || b == ':' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isIdentCont(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}

func readMatcherOp(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", i, false
	}
	if i+1 < len(s) {
		switch s[i : i+2] {
		case "=~", "!~", "!=":
			return s[i : i+2], i + 2, true
		}
	}
	if s[i] == '=' {
		return "=", i + 1, true
	}
	return "", i, false
}

func readQuoted(s string, i int) (string, int, error) {
	if i >= len(s) {
		return "", i, fmt.Errorf("matcher list: expected quoted value")
	}
	quote := s[i]
	if quote != '"' && quote != '\'' && quote != '`' {
		return "", i, fmt.Errorf("matcher list: expected quoted value")
	}
	j := i + 1
	for j < len(s) {
		if s[j] == '\\' && quote != '`' && j+1 < len(s) {
			j += 2
			continue
		}
		if s[j] == quote {
			return s[i : j+1], j + 1, nil
		}
		j++
	}
	return "", i, fmt.Errorf("matcher list: unterminated string")
}
