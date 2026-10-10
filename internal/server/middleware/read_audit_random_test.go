package middleware

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
)

type auditEntropyFailure struct{ reads int }

func (reader *auditEntropyFailure) Read([]byte) (int, error) {
	reader.reads++
	return 0, errors.New("entropy unavailable fixture")
}

func TestReadAuditCryptoSamplingRange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value uint64
		rate  float64
		emit  bool
	}{
		{"zero draw", 0, 0.5, true},
		{"below threshold", 1 << 62, 0.5, true},
		{"exact threshold", 1 << 63, 0.5, true},
		{"above threshold", 3 << 62, 0.5, false},
		{"largest draw", math.MaxUint64, 0.999, false},
		{"largest threshold", math.MaxUint64, math.Nextafter(1, 0), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data [8]byte
			binary.BigEndian.PutUint64(data[:], tc.value)
			evaluator := NewPolicyEvaluator(&fakePolicyStore{})
			reader := bytes.NewReader(data[:])
			evaluator.entropy = reader
			p := policy("sampled", "/sampled", "GET", tc.rate)
			if evaluator.Sample(&p) != tc.emit {
				t.Fatal("sampling threshold changed")
			}
			if reader.Len() != 0 {
				t.Fatal("sampling did not consume one bounded eight-byte draw")
			}
			draw, err := readAuditSampleDraw(bytes.NewReader(data[:]))
			if err != nil || draw < 0 || draw >= 1 {
				t.Fatal("draw outside supported half-open range")
			}
		})
	}
}

func TestReadAuditEntropyFailureSelectsEmission(t *testing.T) {
	for _, reader := range []io.Reader{&auditEntropyFailure{}, bytes.NewReader([]byte{1, 2, 3}), nil} {
		evaluator := NewPolicyEvaluator(&fakePolicyStore{})
		evaluator.entropy = reader
		p := policy("sampled", "/sampled", "GET", 0.5)
		if !evaluator.Sample(&p) {
			t.Fatal("entropy failure suppressed eligible audit emission")
		}
	}
}

func TestReadAuditConfiguredBoundariesDoNotReadEntropy(t *testing.T) {
	for _, tc := range []struct {
		rate float64
		emit bool
	}{{0, false}, {1, true}} {
		reader := &auditEntropyFailure{}
		evaluator := NewPolicyEvaluator(&fakePolicyStore{})
		evaluator.entropy = reader
		p := policy("sampled", "/sampled", "GET", tc.rate)
		if evaluator.Sample(&p) != tc.emit || reader.reads != 0 {
			t.Fatal("configured sampling boundary changed")
		}
		if evaluator.Sample(nil) || reader.reads != 0 {
			t.Fatal("missing policy accessed entropy or selected emission")
		}
	}
}
