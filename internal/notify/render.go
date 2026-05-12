// Template rendering + preview helpers used by both the dispatchers
// and the admin /preview/ endpoint.

package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	htmltemplate "html/template"
	"sort"
	"strings"
	texttemplate "text/template"
)

// RenderResult holds the rendered (subject, body) plus a flag the
// dispatcher uses to decide whether the body needs further markdown→
// html conversion. The dispatcher is the source of truth for that
// step; we expose the format so it doesn't need to re-derive it.
type RenderResult struct {
	Subject    string
	Body       string
	BodyFormat string
}

// Render executes the resolved template against `data`. Subject is
// rendered with text/template; body is rendered with text/template
// EXCEPT for html-format bodies, which use html/template for auto-
// escaping. JSON-format bodies have a `toJSON` funcmap entry added
// so operators can render nested objects without manual quoting.
//
// Errors are wrapped with the key for log correlation.
func Render(res Resolved, data any) (RenderResult, error) {
	out := RenderResult{BodyFormat: res.BodyFormat}
	if res.Subject != "" {
		subj, err := renderText(res.Key+":subject", res.Subject, data)
		if err != nil {
			return out, err
		}
		out.Subject = subj
	}
	if res.BodyFormat == BodyFormatHTML {
		body, err := renderHTML(res.Key+":body", res.Body, data)
		if err != nil {
			return out, err
		}
		out.Body = body
		return out, nil
	}
	body, err := renderText(res.Key+":body", res.Body, data)
	if err != nil {
		return out, err
	}
	out.Body = body
	return out, nil
}

func textFuncs() texttemplate.FuncMap {
	return texttemplate.FuncMap{
		"toJSON": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

func renderText(name, src string, data any) (string, error) {
	tpl, err := texttemplate.New(name).Funcs(textFuncs()).Option("missingkey=zero").Parse(src)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", name, err)
	}
	return buf.String(), nil
}

func renderHTML(name, src string, data any) (string, error) {
	tpl, err := htmltemplate.New(name).Option("missingkey=zero").Parse(src)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute %s: %w", name, err)
	}
	return buf.String(), nil
}

// CheckRequiredVariables returns the names of every Required variable
// in def that is NOT present (or is the empty string) in `sample`.
// The /preview/ endpoint uses this to surface a 400 before invoking
// the template engine, which would happily render a blank value
// (missingkey=zero) and leave the operator confused.
//
// The check is shallow: we accept either "Data.Foo" / "Branding.Foo"
// (email-style nested) or "event_name" (webhook-style flat) as
// variable names. For nested keys we walk one level deep through the
// supplied map.
func CheckRequiredVariables(def TemplateDef, sample map[string]any) []string {
	var missing []string
	for _, v := range def.Variables {
		if !v.Required {
			continue
		}
		if !isProvided(sample, v.Name) {
			missing = append(missing, v.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

func isProvided(sample map[string]any, name string) bool {
	if sample == nil {
		return false
	}
	// Direct top-level match (webhook-style "event_name").
	if v, ok := sample[name]; ok {
		return !isEmpty(v)
	}
	// Nested "Branding.ProductName" / "Data.Username" style — walk
	// at most a few levels deep.
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return false
	}
	cur := any(sample)
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		next, ok := m[p]
		if !ok {
			return false
		}
		cur = next
	}
	return !isEmpty(cur)
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	}
	return false
}
