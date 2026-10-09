package pagination

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/google/uuid"
)

const signedCursorPrefix = "p1."
const signedCursorLifetime = 2 * time.Hour
const maxSignedCursorPayload = 2048
const maxSignedCursorLength = 4096

var ErrCursorSigningUnavailable = errors.New("pagination signing authority unavailable")

// CursorCodec uses the injected JWT key ring as an authority, deriving separate
// pagination keys with a fixed domain. It never reads global/environment state.
// The first key signs; retained keys verify during an explicit operator rotation.
type CursorCodec struct {
	keys [][]byte
	now  func() time.Time
}

func NewCursorCodec(authority string) (*CursorCodec, error) {
	codec := &CursorCodec{now: time.Now}
	for _, raw := range strings.Split(authority, ",") {
		key := strings.TrimSpace(raw)
		if key == "" {
			continue
		}
		derive := hmac.New(sha256.New, []byte(key))
		_, _ = derive.Write([]byte("astronomer/pagination/key/v1"))
		codec.keys = append(codec.keys, derive.Sum(nil))
	}
	if len(codec.keys) == 0 {
		return nil, ErrCursorSigningUnavailable
	}
	return codec, nil
}

// NewTextCursor preserves ascending text/group/ID ordering. The optional group
// is a second UUID sort column, such as a chart's repository ID.
func NewTextCursor(text string, id uuid.UUID, group *uuid.UUID, binding string, seen int) Cursor {
	return Cursor{Version: 2, ID: id, Binding: binding, Seen: seen, Text: &text, Group: group}
}

func (codec *CursorCodec) Encode(cursor Cursor) (string, error) {
	if codec == nil || len(codec.keys) == 0 {
		return "", ErrCursorSigningUnavailable
	}
	if err := validatePosition(cursor, cursor.Binding); err != nil {
		return "", err
	}
	cursor.IssuedAt = codec.now().Unix()
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", ErrInvalidCursor
	}
	if len(raw) > maxSignedCursorPayload {
		return "", ErrInvalidCursor
	}
	body := signedCursorPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + base64.RawURLEncoding.EncodeToString(cursorMAC(codec.keys[0], body)), nil
}

func (codec *CursorCodec) Decode(token, binding string) (Cursor, error) {
	if codec == nil || len(codec.keys) == 0 {
		return Cursor{}, ErrCursorSigningUnavailable
	}
	if len(token) > maxSignedCursorLength || !strings.HasPrefix(token, signedCursorPrefix) {
		return Cursor{}, ErrInvalidCursor
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Cursor{}, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return Cursor{}, ErrInvalidCursor
	}
	body := parts[0] + "." + parts[1]
	valid := false
	for _, key := range codec.keys {
		if hmac.Equal(signature, cursorMAC(key, body)) {
			valid = true
		}
	}
	if !valid {
		return Cursor{}, ErrInvalidCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(raw) > maxSignedCursorPayload {
		return Cursor{}, ErrInvalidCursor
	}
	var cursor Cursor
	if err := jsoncontract.Decode(raw, &cursor); err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	if err := validatePosition(cursor, binding); err != nil {
		return Cursor{}, err
	}
	now := codec.now().UTC()
	issued := time.Unix(cursor.IssuedAt, 0)
	if cursor.IssuedAt <= 0 || issued.After(now.Add(time.Minute)) || now.Sub(issued) > signedCursorLifetime {
		return Cursor{}, ErrInvalidCursor
	}
	return cursor, nil
}

func cursorMAC(key []byte, body string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("astronomer/pagination/token/v1\n" + body))
	return mac.Sum(nil)
}

func validatePosition(cursor Cursor, binding string) error {
	if cursor.ID == uuid.Nil || cursor.Binding == "" || cursor.Binding != binding || cursor.Seen < 0 || cursor.Seen > maxCursorSeen {
		return ErrInvalidCursor
	}
	switch cursor.Version {
	case cursorVersion:
		if cursor.Time.IsZero() || cursor.Text != nil || cursor.Group != nil {
			return ErrInvalidCursor
		}
	case 2:
		if cursor.Text == nil || !utf8.ValidString(*cursor.Text) || utf8.RuneCountInString(*cursor.Text) > 255 || !cursor.Time.IsZero() || (cursor.Group != nil && *cursor.Group == uuid.Nil) {
			return ErrInvalidCursor
		}
	default:
		return ErrInvalidCursor
	}
	return nil
}
