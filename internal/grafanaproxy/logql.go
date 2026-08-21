package grafanaproxy

import (
	"fmt"
	"strings"
)

// injectLogQLMatcher adds or replaces the cluster matcher on every stream
// selector. Queries with no selector fail closed.
func injectLogQLMatcher(expr, label string, clusterIDs []string) (string, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return expr, nil
	}
	ids := normalizeClusterIDs(clusterIDs)
	if len(ids) == 0 {
		return expr, nil
	}

	var edits []spanEdit
	i := 0
	found := false
	for i < len(expr) {
		if isStringStart(expr[i]) {
			next, err := skipQuoted(expr, i)
			if err != nil {
				return "", err
			}
			i = next
			continue
		}
		if expr[i] == '{' {
			end, err := matchingBrace(expr, i)
			if err != nil {
				return "", err
			}
			inner, err := injectMatcherList(expr[i+1:end], label, ids)
			if err != nil {
				return "", err
			}
			edits = append(edits, spanEdit{start: i, end: end + 1, repl: "{" + inner + "}"})
			found = true
			i = end + 1
			continue
		}
		i++
	}
	if !found {
		return "", fmt.Errorf("logql rewrite: no stream selector")
	}
	return applyEdits(expr, edits), nil
}
