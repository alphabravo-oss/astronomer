package handler

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func imageVulnScanTime(value any) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return time.Time{}, false
		}
		return v.UTC(), true
	case pgtype.Timestamptz:
		if !v.Valid || v.Time.IsZero() {
			return time.Time{}, false
		}
		return v.Time.UTC(), true
	case *time.Time:
		if v == nil || v.IsZero() {
			return time.Time{}, false
		}
		return v.UTC(), true
	case *pgtype.Timestamptz:
		if v == nil || !v.Valid || v.Time.IsZero() {
			return time.Time{}, false
		}
		return v.Time.UTC(), true
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil || t.IsZero() {
			return time.Time{}, false
		}
		return t.UTC(), true
	default:
		return time.Time{}, false
	}
}
