// Package safeconv holds the bounded integer conversions used wherever a
// value crosses an integer width or signedness (gosec G115). Every helper
// saturates: a value below the target's range becomes its minimum, a value
// above becomes its maximum. Callers convert values that are already bounded
// by validation (page sizes, replica counts, byte sizes, uids), so saturation
// only ever turns a would-be silent wrap-around into a visible, bounded
// extreme instead of a negative or tiny number.
package safeconv

import "math"

// Integer is every built-in integer kind.
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// bounds reports v clamped to [lo, hi] as either a non-negative uint64 or a
// negative int64, so the caller can build the target value without an
// unchecked narrowing.
func clampSigned[T Integer](v T, lo, hi int64) int64 {
	if v < 0 {
		// v is negative, so T is signed and fits in int64.
		n := int64(v) // #nosec G115 -- v < 0 implies a signed T of at most 64 bits
		if n < lo {
			return lo
		}
		return n
	}
	u := uint64(v) // #nosec G115 -- v >= 0, any non-negative integer fits uint64
	if hi < 0 || u > uint64(hi) {
		return hi
	}
	return int64(u) // #nosec G115 -- u <= hi <= math.MaxInt64
}

func clampUnsigned[T Integer](v T, hi uint64) uint64 {
	if v < 0 {
		return 0
	}
	u := uint64(v) // #nosec G115 -- v >= 0, any non-negative integer fits uint64
	if u > hi {
		return hi
	}
	return u
}

// Int32 converts v to int32, saturating at math.MinInt32/math.MaxInt32.
func Int32[T Integer](v T) int32 {
	return int32(clampSigned(v, math.MinInt32, math.MaxInt32)) // #nosec G115 -- clamped to the int32 range
}

// Int64 converts v to int64, saturating at math.MaxInt64 for large unsigned values.
func Int64[T Integer](v T) int64 {
	return clampSigned(v, math.MinInt64, math.MaxInt64)
}

// Int converts v to int, saturating at the platform int range.
func Int[T Integer](v T) int {
	return int(clampSigned(v, math.MinInt, math.MaxInt)) // #nosec G115 -- clamped to the int range
}

// Uint32 converts v to uint32, saturating at 0 and math.MaxUint32.
func Uint32[T Integer](v T) uint32 {
	return uint32(clampUnsigned(v, math.MaxUint32)) // #nosec G115 -- clamped to the uint32 range
}

// Uint64 converts v to uint64, saturating negative values at 0.
func Uint64[T Integer](v T) uint64 {
	return clampUnsigned(v, math.MaxUint64)
}

// Byte converts v to byte, saturating at 0 and 255.
func Byte[T Integer](v T) byte {
	return byte(clampUnsigned(v, math.MaxUint8)) // #nosec G115 -- clamped to the byte range
}
