package pagination

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLegacyCursorRejectsSignedOnlyFieldsEvenWhenEmpty(t *testing.T) {
	binding := Binding("clusters")
	position := NewCursor(time.Now(), uuid.New(), binding, 0)
	token, err := EncodeCursor(position)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"k":null`, `"g":null`, `"iat":0`} {
		t.Run(field, func(t *testing.T) {
			extended := append(append([]byte{}, raw[:len(raw)-1]...), []byte(","+field+"}")...)
			if _, err := DecodeCursor(base64.RawURLEncoding.EncodeToString(extended), binding); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("legacy cursor accepted signed-only field: %v", err)
			}
		})
	}
}

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
