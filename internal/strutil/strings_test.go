package strutil

import "testing"

func TestFirstNonBlank(t *testing.T) {
	if got := FirstNonBlank("", " \t ", " value ", "next"); got != " value " {
		t.Fatalf("FirstNonBlank = %q, want original non-blank value", got)
	}
	if got := FirstNonBlank("", "   "); got != "" {
		t.Fatalf("FirstNonBlank(blank) = %q, want empty", got)
	}
}

func TestFirstNonBlankTrimmed(t *testing.T) {
	if got := FirstNonBlankTrimmed("", " \t ", " value ", "next"); got != "value" {
		t.Fatalf("FirstNonBlankTrimmed = %q, want trimmed value", got)
	}
	if got := FirstNonBlankTrimmed("", "   "); got != "" {
		t.Fatalf("FirstNonBlankTrimmed(blank) = %q, want empty", got)
	}
}
