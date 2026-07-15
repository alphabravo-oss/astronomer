package sessionpolicy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMinutes(t *testing.T) {
	tests := []struct {
		name    string
		raw     json.RawMessage
		want    int
		wantErr string
	}{
		{name: "missing", want: DefaultMinutes},
		{name: "null", raw: json.RawMessage("null"), want: DefaultMinutes},
		{name: "explicit", raw: json.RawMessage("120"), want: 120},
		{name: "minimum", raw: json.RawMessage("5"), want: MinMinutes},
		{name: "maximum", raw: json.RawMessage("10080"), want: MaxMinutes},
		{name: "malformed", raw: json.RawMessage(`"120"`), want: DefaultMinutes, wantErr: "JSON integer"},
		{name: "fractional", raw: json.RawMessage("120.5"), want: DefaultMinutes, wantErr: "integer"},
		{name: "below minimum", raw: json.RawMessage("4"), want: DefaultMinutes, wantErr: "between 5 and 10080"},
		{name: "above maximum", raw: json.RawMessage("10081"), want: DefaultMinutes, wantErr: "between 5 and 10080"},
		{name: "trailing data", raw: json.RawMessage("60 120"), want: DefaultMinutes, wantErr: "exactly one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMinutes(tt.raw)
			if got != tt.want {
				t.Fatalf("ParseMinutes() = %d, want %d", got, tt.want)
			}
			if tt.wantErr == "" && err != nil {
				t.Fatalf("ParseMinutes() unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("ParseMinutes() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
