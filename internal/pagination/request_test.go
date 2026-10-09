package pagination

import (
	"errors"
	"net/url"
	"testing"
)

func TestLegacyOffsetProgressBoundary(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want int32
		err  error
	}{
		{"", 0, nil}, {"0", 0, nil}, {"10000", 10000, nil}, {"-1", 0, nil}, {"invalid", 0, nil},
		{"10020", 0, ErrOffsetNotSupported}, {"2147483648", 0, ErrOffsetNotSupported}, {"18446744073709551615", 0, ErrOffsetNotSupported}, {"9999999999999999999999999", 0, ErrOffsetNotSupported},
	} {
		got, err := LegacyOffset(url.Values{"offset": {test.raw}})
		if got != test.want || !errors.Is(err, test.err) {
			t.Fatalf("offset%s = %d,%v want%d,%v", test.raw, got, err, test.want, test.err)
		}
	}
	if _, err := LegacyOffset(url.Values{"offset": {"1", "10020"}}); !errors.Is(err, ErrRepeatedOffset) {
		t.Fatalf("ambiguous offset error%v", err)
	}
}
