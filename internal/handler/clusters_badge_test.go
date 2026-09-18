package handler

import "testing"

func TestNormalizeClusterBadge(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		text      string
		color     string
		wantText  string
		wantColor string
		wantErr   bool
	}{
		{name: "empty", wantText: "", wantColor: ""},
		{name: "trim and normalize", text: " Production ", color: " RED ", wantText: "Production", wantColor: "red"},
		{name: "default color", text: "Preview", wantText: "Preview", wantColor: "slate"},
		{name: "color without text", color: "red", wantErr: true},
		{name: "unknown color", text: "Prod", color: "pink", wantErr: true},
		{name: "long text", text: "this badge label is far too long", color: "red", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			text, color, err := normalizeClusterBadge(test.text, test.color)
			if (err != nil) != test.wantErr {
				t.Fatalf("normalizeClusterBadge() error = %v, wantErr %v", err, test.wantErr)
			}
			if text != test.wantText || color != test.wantColor {
				t.Fatalf("normalizeClusterBadge() = (%q, %q), want (%q, %q)", text, color, test.wantText, test.wantColor)
			}
		})
	}
}
