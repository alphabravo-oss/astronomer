package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClosedAuthoritySchema(t *testing.T) {
	raw, err := os.ReadFile("../versions.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown root field", func(d map[string]any) { d["unchecked"] = true }},
		{"unknown component field", func(d map[string]any) { d["components"].([]any)[0].(map[string]any)["unchecked"] = true }},
		{"missing owner", func(d map[string]any) { delete(d["components"].([]any)[0].(map[string]any), "owner") }},
		{"prerelease declaration", func(d map[string]any) {
			d["components"].([]any)[0].(map[string]any)["discovery"].(map[string]any)["stable"] = false
		}},
		{"invalid upstream", func(d map[string]any) {
			d["components"].([]any)[0].(map[string]any)["upstream"].(map[string]any)["url"] = "http://untrusted.invalid"
		}},
		{"invalid digest", func(d map[string]any) {
			for _, c := range d["components"].([]any) {
				a := c.(map[string]any)["artifacts"].([]any)
				if len(a) > 0 {
					a[0].(map[string]any)["digest"] = "mutable:latest"
					break
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			tc.mutate(document)
			path := filepath.Join(t.TempDir(), "inventory.json")
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := validate("../versions.schema.json", path); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
	if err := validate("../versions.schema.json", "../versions.json"); err != nil {
		t.Fatal(err)
	}
}
