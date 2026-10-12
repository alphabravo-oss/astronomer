package pagination

import "testing"

func TestCursorUncountedEmptyCandidateProgress(t *testing.T) {
	page := CursorUncounted(20, 0, 0, "signed-advance")
	if page.Total != nil || !page.HasMore || page.NextCursor == nil || *page.NextCursor != "signed-advance" || page.NextOffset == nil || *page.NextOffset != 0 {
		t.Fatal("empty authorized window lost its advancing cursor")
	}
	terminal := CursorUncounted(20, 0, 0, "")
	if terminal.HasMore || terminal.NextCursor != nil || terminal.NextOffset != nil {
		t.Fatal("terminal empty window invented continuation")
	}
	offset := Uncounted(20, 0, 0, true)
	if offset.HasMore || offset.NextOffset != nil {
		t.Fatal("candidate semantics changed legacy offset behavior")
	}
}
