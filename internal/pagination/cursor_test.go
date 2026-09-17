package pagination

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTripAndBinding(t *testing.T) {
	binding := Binding("clusters", "status=active", "scope=all")
	want := NewCursor(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC), uuid.New(), binding, 50)
	token, err := EncodeCursor(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCursor(token, binding)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
	if _, err := DecodeCursor(token, Binding("clusters", "status=failed", "scope=all")); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("filter-substituted cursor error = %v", err)
	}
}

func TestCursorRejectsMalformedAndUnknownFields(t *testing.T) {
	binding := Binding("clusters")
	for _, token := range []string{
		"not-base64",
		strings.Repeat("x", maxCursorLength+1),
		"eyJ2IjoxLCJ0IjoiMjAyNi0wOS0xN1QxMjowMDowMFoiLCJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMSIsImIiOiJ4IiwibiI6MCwiZXh0cmEiOnRydWV9",
	} {
		if _, err := DecodeCursor(token, binding); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("DecodeCursor(%q) error = %v", token, err)
		}
	}
}

func TestSortedUUIDsIsOrderIndependent(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if SortedUUIDs([]uuid.UUID{a, b}) != SortedUUIDs([]uuid.UUID{b, a}) {
		t.Fatal("authorization scope binding depends on database order")
	}
}
