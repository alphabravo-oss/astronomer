package charliequalification

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestHookLifecycleLogsUseClosedContentFreeSchemas(t *testing.T) {
	for _, test := range []struct {
		event HookLifecycleEvent
		keys  []string
	}{
		{event: HookStarted, keys: []string{"time", "level", "msg", "event", "outcome_code", "transport"}},
		{event: HookStoppedWithFailure, keys: []string{"time", "level", "msg", "event", "outcome_code", "failure_code"}},
	} {
		var output bytes.Buffer
		LogHookLifecycle(slog.New(slog.NewJSONHandler(&output, nil)), test.event)
		if strings.Contains(output.String(), "password") || strings.Contains(output.String(), "provider") || strings.Contains(output.String(), "prompt") {
			t.Fatalf("qualification log exposed content-shaped data: %s", output.String())
		}
		var row map[string]any
		if err := json.Unmarshal(output.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(row))
		for key := range row {
			got = append(got, key)
		}
		sort.Strings(got)
		want := append([]string(nil), test.keys...)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("qualification log schema drifted: got=%v want=%v row=%s", got, want, output.String())
		}
	}

	var output bytes.Buffer
	LogHookLifecycle(slog.New(slog.NewJSONHandler(&output, nil)), HookLifecycleEvent(255))
	if output.Len() != 0 {
		t.Fatalf("unknown lifecycle value produced a log: %s", output.String())
	}
}
