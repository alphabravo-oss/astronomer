package pagination

import (
	"errors"
	"net/url"
	"strconv"
)

// MaxLegacyOffset bounds the rows discarded by a legacy SQL page. Collections
// with deeper traversal expose an opaque cursor instead of increasing this cap.
const MaxLegacyOffset uint64 = 10_000

var ErrOffsetNotSupported = errors.New("offset exceeds the supported legacy pagination range")
var ErrRepeatedOffset = errors.New("offset must be one value")

// LegacyOffset preserves the v1 fallback for absent, malformed or negative
// values. An oversized offset is an explicit error; it must never clamp an
// accepted continuation backwards to a previously returned page.
func LegacyOffset(values url.Values) (int32, error) {
	raw, present := values["offset"]
	if !present {
		return 0, nil
	}
	if len(raw) != 1 {
		return 0, ErrRepeatedOffset
	}
	value, err := strconv.ParseUint(raw[0], 10, 64)
	if err != nil {
		var number *strconv.NumError
		if errors.As(err, &number) && errors.Is(number.Err, strconv.ErrRange) {
			return 0, ErrOffsetNotSupported
		}
		return 0, nil
	}
	if value > MaxLegacyOffset {
		return 0, ErrOffsetNotSupported
	}
	return int32(value), nil
}
