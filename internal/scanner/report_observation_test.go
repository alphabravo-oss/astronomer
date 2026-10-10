package scanner

import (
	"testing"
	"time"
)

func TestReportTimeObservationNeverUsesLegacyReceiptFallback(t *testing.T) {
	for _, value := range []string{"", "not-a-date", " ", "2026-13-10T12:00:00Z"} {
		t.Run(value, func(t *testing.T) {
			before := time.Now().UTC()
			legacy := parseRFC3339OrNow(value)
			observed := observeReportTime(value)
			if observed.UpstreamTime != nil || observed.TimeState == "valid" {
				t.Fatal("receipt fallback minted upstream time")
			}
			expected := "invalid"
			if value == "" {
				expected = "missing"
			}
			if observed.TimeState != expected {
				t.Fatal("absent and invalid fields conflated")
			}
			if legacy.Before(before) || legacy.After(time.Now().UTC()) {
				t.Fatal("legacy receipt fallback changed")
			}
		})
	}
}

func TestReportTimeObservationKeepsParsedClockWithoutFreshnessInference(t *testing.T) {
	for _, value := range []string{"2026-10-10T12:00:00Z", "2026-10-10T14:00:00+02:00", "2026-10-10T12:00:00.123456Z", "2099-10-10T12:00:00Z", "2000-10-10T12:00:00Z"} {
		t.Run(value, func(t *testing.T) {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				t.Fatal(err)
			}
			observation := observeReportTime(value)
			if observation.TimeState != "valid" || observation.UpstreamTime == nil || !observation.UpstreamTime.Equal(parsed) || observation.UpstreamTime.Location() != time.UTC {
				t.Fatal("original clock not retained in UTC")
			}
			if !parseRFC3339OrNow(value).Equal(parsed) {
				t.Fatal("legacy valid clock changed")
			}
			// Future/old parsed values remain observations, not proof of freshness.
		})
	}
}
