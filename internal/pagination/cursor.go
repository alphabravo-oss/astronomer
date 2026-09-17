package pagination

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	cursorVersion   = 1
	maxCursorLength = 1024
	maxCursorSeen   = 1_000_000_000
)

var ErrInvalidCursor = errors.New("invalid pagination cursor")

// Cursor is an opaque continuation position for a descending (timestamp, ID)
// keyset. Binding ties it to the exact authorization scope, filters, and sort
// contract that produced it; Seen preserves backward-compatible page metadata
// without ever sending that offset to SQL.
type Cursor struct {
	Version int       `json:"v"`
	Time    time.Time `json:"t"`
	ID      uuid.UUID `json:"id"`
	Binding string    `json:"b"`
	Seen    int       `json:"n"`
}

func NewCursor(at time.Time, id uuid.UUID, binding string, seen int) Cursor {
	return Cursor{Version: cursorVersion, Time: at.UTC(), ID: id, Binding: binding, Seen: seen}
}

func EncodeCursor(cursor Cursor) (string, error) {
	if err := validateCursor(cursor, cursor.Binding); err != nil {
		return "", err
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeCursor(value, binding string) (Cursor, error) {
	if value == "" || len(value) > maxCursorLength {
		return Cursor{}, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor Cursor
	if err := decoder.Decode(&cursor); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if err := ensureCursorEOF(decoder); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if err := validateCursor(cursor, binding); err != nil {
		return Cursor{}, err
	}
	return cursor, nil
}

func validateCursor(cursor Cursor, binding string) error {
	if cursor.Version != cursorVersion || cursor.Time.IsZero() || cursor.ID == uuid.Nil ||
		cursor.Seen < 0 || cursor.Seen > maxCursorSeen || cursor.Binding == "" || cursor.Binding != binding {
		return ErrInvalidCursor
	}
	return nil
}

func ensureCursorEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidCursor
	}
	return nil
}

// Binding returns a fixed-width digest over ordered, length-delimited values.
// Callers sort set-like inputs (such as authorized cluster IDs) first.
func Binding(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = fmt.Fprintf(hash, "%d:%s\n", len(value), value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func SortedUUIDs(values []uuid.UUID) string {
	items := make([]string, len(values))
	for i, value := range values {
		items[i] = value.String()
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

// CursorPage preserves the canonical envelope while exposing a keyset token.
func CursorPage(total int64, limit, seen, pageLen int, nextCursor string) Metadata {
	metadata := Exact(total, limit, seen, pageLen)
	metadata.HasMore = false
	metadata.NextOffset = nil
	if nextCursor != "" {
		metadata.HasMore = true
		metadata.NextCursor = &nextCursor
		next := seen + pageLen
		metadata.NextOffset = &next
	}
	return metadata
}
