package maintenance

import (
	"testing"
	"time"
)

// IsActive must stay correct after the cheap-lookback rewrite: the old code
// walked 366 days of cron fires (≈525k iterations for a `* * * * *` cron) on
// every gated mutation; the new code looks back exactly one duration. Correct
// active/inactive answers must be preserved for frequent crons — the exact case
// that made the old walk pathological.
func TestIsActive_FrequentCronCorrectness(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := Window{CronOpen: "*/5 * * * *", DurationMinutes: 2, Timezone: "UTC"}

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"at fire :05:00", base.Add(5 * time.Minute), true},
		{"1min after fire :06:00 within 2m window", base.Add(6 * time.Minute), true},
		{"3min after fire :08:00 past 2m window", base.Add(8 * time.Minute), false},
		{"just before boundary :06:59", base.Add(6*time.Minute + 59*time.Second), true},
		{"exactly at close :07:00", base.Add(7 * time.Minute), false},
	}
	for _, tc := range cases {
		got, err := IsActive(w, tc.now)
		if err != nil {
			t.Fatalf("%s: IsActive error: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: IsActive = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The every-minute cron is the worst case for the old 366-day walk. Verify the
// active answer is still correct (and the call returns promptly rather than
// grinding through hundreds of thousands of fires).
func TestIsActive_EveryMinuteCron(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 30, 0, time.UTC)
	w := Window{CronOpen: "* * * * *", DurationMinutes: 1, Timezone: "UTC"}
	got, err := IsActive(w, now)
	if err != nil {
		t.Fatalf("IsActive error: %v", err)
	}
	if !got {
		t.Fatal("every-minute cron with 1m duration must be active 30s after a fire")
	}
}

// NextClose must return lastOpen+duration for the currently-active window,
// using the same cheap lookback (previously it repeated the 366-day walk).
func TestNextClose_ActiveFrequentCron(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	w := Window{CronOpen: "*/5 * * * *", DurationMinutes: 2, Timezone: "UTC"}
	now := base.Add(6 * time.Minute) // active: last open 00:05, close 00:07
	got := NextClose(w, now)
	want := base.Add(7 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("NextClose = %s, want %s", got, want)
	}
}
