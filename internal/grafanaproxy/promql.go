package grafanaproxy

import (
	"fmt"
	"strings"
)

// injectPromQLMatcher adds or replaces label matchers on every vector selector.
// Expressions with no selectors (time(), vector(1)) are left unchanged.
func injectPromQLMatcher(expr, label string, clusterIDs []string) (string, error) {
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
	for i < len(expr) {
		i = skipPromSpaceAndComments(expr, i)
		if i >= len(expr) {
			break
		}
		if isStringStart(expr[i]) {
			next, err := skipQuoted(expr, i)
			if err != nil {
				return "", err
			}
			i = next
			continue
		}
		if expr[i] >= '0' && expr[i] <= '9' {
			i = skipNumberOrDuration(expr, i)
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
			i = end + 1
			continue
		}
		if isIdentStart(expr[i]) {
			name, next, _ := readIdent(expr, i)
			afterName := skipPromSpaceAndComments(expr, next)
			switch {
			case isGroupingKeyword(name):
				i = skipGroupingParens(expr, afterName)
			case isPromKeyword(name):
				i = next
			case isAggregator(name) && aggregatorLooksLikeOp(expr, afterName):
				i = next
			default:
				if afterName < len(expr) && expr[afterName] == '(' {
					i = next
					continue
				}
				if afterName < len(expr) && expr[afterName] == '{' {
					i = next
					continue
				}
				inner, err := injectMatcherList("", label, ids)
				if err != nil {
					return "", err
				}
				edits = append(edits, spanEdit{start: next, end: next, repl: "{" + inner + "}"})
				i = next
			}
			continue
		}
		i++
	}

	out := applyEdits(expr, edits)
	if !promQLHasLabel(out, label) && promQLNeedsSelector(expr) {
		return "", fmt.Errorf("promql rewrite did not inject %s", label)
	}
	return out, nil
}

type spanEdit struct {
	start, end int
	repl       string
}

func applyEdits(s string, edits []spanEdit) string {
	if len(edits) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, e := range edits {
		if e.start < last {
			continue
		}
		b.WriteString(s[last:e.start])
		b.WriteString(e.repl)
		last = e.end
	}
	b.WriteString(s[last:])
	return b.String()
}

func skipPromSpaceAndComments(s string, i int) int {
	for i < len(s) {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r':
			i++
		case s[i] == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		default:
			return i
		}
	}
	return i
}

func isStringStart(b byte) bool {
	return b == '"' || b == '\'' || b == '`'
}

func skipQuoted(s string, i int) (int, error) {
	_, next, err := readQuoted(s, i)
	return next, err
}

func matchingBrace(s string, i int) (int, error) {
	if i >= len(s) || s[i] != '{' {
		return i, fmt.Errorf("promql: expected '{'")
	}
	j := i + 1
	for j < len(s) {
		j = skipPromSpaceAndComments(s, j)
		if j >= len(s) {
			break
		}
		if isStringStart(s[j]) {
			next, err := skipQuoted(s, j)
			if err != nil {
				return i, err
			}
			j = next
			continue
		}
		if s[j] == '}' {
			return j, nil
		}
		j++
	}
	return i, fmt.Errorf("promql: unterminated selector")
}

func skipGroupingParens(s string, i int) int {
	i = skipPromSpaceAndComments(s, i)
	if i >= len(s) || s[i] != '(' {
		return i
	}
	depth := 0
	for i < len(s) {
		if isStringStart(s[i]) {
			next, err := skipQuoted(s, i)
			if err != nil {
				return i + 1
			}
			i = next
			continue
		}
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return i
}

func aggregatorLooksLikeOp(s string, i int) bool {
	i = skipPromSpaceAndComments(s, i)
	if i >= len(s) {
		return false
	}
	if s[i] == '(' {
		return true
	}
	if name, _, ok := readIdent(s, i); ok {
		return name == "by" || name == "without"
	}
	return false
}

func isGroupingKeyword(name string) bool {
	switch name {
	case "by", "without", "on", "ignoring", "group_left", "group_right":
		return true
	}
	return false
}

func isPromKeyword(name string) bool {
	switch name {
	case "and", "or", "unless", "offset", "bool", "inf", "nan", "atan2":
		return true
	}
	return false
}

func isAggregator(name string) bool {
	switch name {
	case "sum", "min", "max", "avg", "group", "stddev", "stdvar",
		"count", "count_values", "bottomk", "topk", "quantile",
		"limitk", "limit_ratio":
		return true
	}
	return false
}

func promQLHasLabel(expr, label string) bool {
	return strings.Contains(expr, label+"=") || strings.Contains(expr, label+"=~")
}

func promQLNeedsSelector(expr string) bool {
	for i := 0; i < len(expr); {
		i = skipPromSpaceAndComments(expr, i)
		if i >= len(expr) {
			break
		}
		if isStringStart(expr[i]) {
			next, err := skipQuoted(expr, i)
			if err != nil {
				return true
			}
			i = next
			continue
		}
		if expr[i] == '{' {
			return true
		}
		if isIdentStart(expr[i]) {
			name, next, _ := readIdent(expr, i)
			after := skipPromSpaceAndComments(expr, next)
			if isGroupingKeyword(name) || isPromKeyword(name) {
				i = next
				continue
			}
			if isAggregator(name) && aggregatorLooksLikeOp(expr, after) {
				i = next
				continue
			}
			if after < len(expr) && expr[after] == '(' {
				i = next
				continue
			}
			if isFunctionName(name) {
				i = next
				continue
			}
			return true
		}
		i++
	}
	return false
}

func skipNumberOrDuration(s string, i int) int {
	if i >= len(s) || s[i] < '0' || s[i] > '9' {
		return i
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			i = j
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
		}
	}
	if i < len(s) && isIdentStart(s[i]) {
		name, next, ok := readIdent(s, i)
		if ok && isDurationUnit(name) {
			return next
		}
	}
	return i
}

func isDurationUnit(name string) bool {
	switch name {
	case "ms", "s", "m", "h", "d", "w", "y":
		return true
	}
	return false
}

func isFunctionName(name string) bool {
	switch name {
	case "time", "vector", "scalar", "abs", "absent", "absent_over_time",
		"ceil", "clamp", "clamp_max", "clamp_min", "exp", "floor", "ln",
		"log2", "log10", "round", "sqrt", "changes", "delta", "deriv",
		"holt_winters", "idelta", "increase", "irate", "predict_linear",
		"rate", "resets", "avg_over_time", "min_over_time", "max_over_time",
		"sum_over_time", "count_over_time", "stddev_over_time",
		"stdvar_over_time", "last_over_time", "present_over_time",
		"quantile_over_time", "mad_over_time", "histogram_quantile",
		"histogram_sum", "histogram_count", "histogram_avg",
		"histogram_fraction", "histogram_stddev", "label_replace",
		"label_join", "sort", "sort_desc", "sort_by_label",
		"sort_by_label_desc", "sgn", "pi", "start", "end", "day_of_month",
		"day_of_week", "day_of_year", "days_in_month", "hour", "minute",
		"month", "year", "timestamp":
		return true
	}
	return false
}
