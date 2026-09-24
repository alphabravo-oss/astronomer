package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUnsupportedIntentFailsBeforeEffects(t *testing.T) {
	for _, field := range []string{"registries", "toolPresets"} {
		t.Run(field, func(t *testing.T) {
			content := []byte("apiVersion: " + APIVersion + "\nkind: " + Kind + "\nmetadata:\n  name: prod\nspec:\n  " + field + ": [requested]\n")
			_, err := Parse(content, "cluster.yaml")
			if !errors.Is(err, ErrUnsupportedIntent) || IsSkippable(err) || !strings.Contains(err.Error(), "spec."+field) {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if _, err = ParseAll(content, "cluster.yaml"); !errors.Is(err, ErrUnsupportedIntent) {
				t.Fatal(err)
			}
			var doc ClusterRegistration
			doc.Metadata.Name = "prod"
			if field == "registries" {
				doc.Spec.Registries = []string{"requested"}
			} else {
				doc.Spec.ToolPresets = []string{"requested"}
			}
			// A nil querier proves neither normal nor dry-run Apply touches storage.
			for _, dry := range []bool{false, true} {
				if _, err = Apply(context.Background(), nil, ApplyInput{Doc: doc, Dry: dry}); !errors.Is(err, ErrUnsupportedIntent) {
					t.Fatal(err)
				}
			}
		})
	}
}
