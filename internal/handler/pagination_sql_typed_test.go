package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTypedSQLPaginationMatchesLegacyBounds(t *testing.T) {
	for _, value := range []string{"", "42", "0", "-1", "-99999999999999999999999999", "invalid", "+42", "2147483648", "9223372036854775807", "9223372036854775808", "18446744073709551616"} {
		r := httptest.NewRequest(http.MethodGet, "/?"+url.Values{"limit": {value}, "offset": {value}}.Encode(), nil)
		for _, defaults := range []struct{ fallback, ceiling int32 }{{20, 200}, {100, 500}, {0, 0}, {-1, -1}, {500, 200}, {2147483647, 500}, {2147483647, 2147483647}} {
			got := queryLimitMax32(r, defaults.fallback, defaults.ceiling)
			want := queryLimitMax(r, int(defaults.fallback), int(defaults.ceiling))
			if int(got) != want || got < 1 {
				t.Fatalf("limit %q default=%d max=%d: typed=%d legacy=%d", value, defaults.fallback, defaults.ceiling, got, want)
			}
		}
		if got, want := queryLimit32(r, 20), queryLimit(r, 20); int(got) != want {
			t.Fatalf("limit %q: typed=%d legacy=%d", value, got, want)
		}
		if got, want := queryOffset32(r), queryOffset(r); int(got) != want || got < 0 || got > 10000 {
			t.Fatalf("offset %q: typed=%d legacy=%d", value, got, want)
		}
	}
}
