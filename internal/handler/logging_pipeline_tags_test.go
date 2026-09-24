package handler

import (
	"path"
	"strings"
	"testing"
)

func TestPipelineRewriteUsesTailRecordField(t *testing.T) {
	config := renderPipelineBlock(loggingOperationEnvelope{
		TargetID: "02228a60-e5f4-4f75-8780-cb97fd7ecf58", Enabled: true,
	})
	// Tail produces a string `log` field, including for unparsed CRI lines.
	// $TAG is a new-tag substitution, not an existing record field to match.
	if !strings.Contains(config, "Rule $log ^.*$ astronomer.pipeline.") || strings.Contains(config, "Rule $TAG") {
		t.Fatalf("rewrite rule cannot emit tail records: %s", config)
	}
}

func TestPipelineMatchPatternsMatchRealTailTags(t *testing.T) {
	patterns := pipelineMatchPatterns([]string{"team-a"})
	if len(patterns) != 1 {
		t.Fatalf("patterns=%v", patterns)
	}
	for _, tc := range []struct {
		tag  string
		want bool
	}{
		{"kube.var.log.containers.web-123_team-a_web-abcdef.log", true},
		{"kube.var.log.containers.web-123_team-a-staging_web-abcdef.log", false},
		{"kube.var.log.containers.team-a_other_web-abcdef.log", false},
		{"astronomer.pipeline.already-routed", false},
	} {
		got, err := path.Match(patterns[0], tc.tag)
		if err != nil || got != tc.want {
			t.Errorf("tag=%s got=%v want=%v err=%v", tc.tag, got, tc.want, err)
		}
	}
	if got := pipelineMatchPatterns([]string{"*", "../other", ""}); len(got) != 0 {
		t.Fatalf("invalid namespace selection broadened to %v", got)
	}
	if got := pipelineMatchPatterns(nil); len(got) != 1 || got[0] != "kube.*" {
		t.Fatalf("explicit all-namespace selection=%v", got)
	}
}
