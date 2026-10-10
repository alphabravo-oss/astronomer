package middleware

import (
	"encoding/binary"
	"io"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Sample selects a read-audit emission using an unpredictable draw. Configured
// zero and one retain their exact behavior without touching entropy. A failed
// draw selects emission, preserving audit coverage rather than omitting it.
func (e *PolicyEvaluator) Sample(p *sqlc.ReadAuditPolicy) bool {
	if p == nil || p.SampleRate <= 0 {
		return false
	}
	if p.SampleRate >= 1 {
		return true
	}
	draw, err := readAuditSampleDraw(e.entropy)
	if err != nil {
		return true
	}
	return draw <= p.SampleRate
}

func readAuditSampleDraw(entropy io.Reader) (float64, error) {
	var bytes [8]byte
	if entropy == nil {
		return 0, io.ErrUnexpectedEOF
	}
	if _, err := io.ReadFull(entropy, bytes[:]); err != nil {
		return 0, err
	}
	// The high 53 bits form an exactly representable uniform draw in [0,1).
	return float64(binary.BigEndian.Uint64(bytes[:])>>11) / (1 << 53), nil
}
