package pagination

import (
	"encoding/base64"
	"errors"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestSignedCursorAuthorityRotationScopeAndExpiry(t *testing.T) {
	old, err := NewCursorCodec("fixture-old-authority")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 23, 0, 0, 0, time.UTC)
	old.now = func() time.Time { return now }
	binding := Binding("actor1", "project1", "name:asc,id:asc", "search=literal")
	token, err := old.Encode(NewCursor(now, uuid.New(), binding, 10020))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewCursorCodec("fixture-new-authority,fixture-old-authority")
	if err != nil {
		t.Fatal(err)
	}
	rotated.now = old.now
	got, err := rotated.Decode(token, binding)
	if err != nil || got.Seen != 10020 {
		t.Fatalf("retained key failed %v", err)
	}
	removed, err := NewCursorCodec("fixture-new-authority")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := removed.Decode(token, binding); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("removed key accepted%v", err)
	}
	for _, changed := range []string{Binding("actor2", "project1", "name:asc,id:asc", "search=literal"), Binding("actor1", "project2", "name:asc,id:asc", "search=literal"), Binding("actor1", "project1", "name:desc,id:desc", "search=literal"), Binding("actor1", "project1", "name:asc,id:asc", "search=other")} {
		if _, err := old.Decode(token, changed); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("scope substitution accepted%v", err)
		}
	}
	old.now = func() time.Time { return now.Add(signedCursorLifetime + time.Second) }
	if _, err := old.Decode(token, binding); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("stale cursor accepted%v", err)
	}
	old.now = func() time.Time { return now.Add(-2 * time.Minute) }
	if _, err := old.Decode(token, binding); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("future cursor accepted%v", err)
	}
}

func TestSignedCursorTamperTruncationAndCanonicalPosition(t *testing.T) {
	codec, err := NewCursorCodec("fixture-pagination-authority")
	if err != nil {
		t.Fatal(err)
	}
	group := uuid.New()
	binding := Binding("catalog", "actor1", "global", "name:asc,repository:asc,id:asc")
	token, err := codec.Encode(NewTextCursor("same-name", uuid.New(), &group, binding, 20))
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(token, binding)
	if err != nil || got.Text == nil || *got.Text != "same-name" || got.Group == nil || *got.Group != group {
		t.Fatalf("text position lost%+v %v", got, err)
	}
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(raw), "same-name", "othername", 1)
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(changed))
	for _, value := range []string{strings.Join(parts, "."), token[:len(token)-1], token + ".extra", "p1.invalid.invalid", strings.Repeat("x", 4097)} {
		if _, err := codec.Decode(value, binding); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("tampered/truncated accepted%v", err)
		}
	}
	if _, err := DecodeCursor(token, binding); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("signed token accepted as legacy%v", err)
	}
	if _, err := codec.Encode(NewTextCursor(strings.Repeat("x", 257), uuid.New(), nil, binding, 0)); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("unbounded key accepted%v", err)
	}
	wide, err := codec.Encode(NewTextCursor(strings.Repeat("星", 255), uuid.New(), nil, binding, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode(wide, binding); err != nil {
		t.Fatal(err)
	}
	var missing *CursorCodec
	if _, err := missing.Encode(NewCursor(time.Now(), uuid.New(), binding, 0)); !errors.Is(err, ErrCursorSigningUnavailable) {
		t.Fatalf("unsigned emission%v", err)
	}
	if _, err := NewCursorCodec(" , "); !errors.Is(err, ErrCursorSigningUnavailable) {
		t.Fatalf("missing authority accepted%v", err)
	}
}
