package middleware

import (
	"math/rand"
	"testing"
)

// This characterization records the security distinction from reconnect
// jitter: knowing a time-seeded audit RNG state replays every emission choice.
// It is a before-fix observation, not an acceptance test for that behavior.
func TestReadAuditLegacySeedReplaysSamplingDecisions(t *testing.T) {
	const seed int64 = 42
	evaluator := NewPolicyEvaluator(&fakePolicyStore{})
	evaluator.rng = rand.New(rand.NewSource(seed))
	prediction := rand.New(rand.NewSource(seed))
	p := policy("sampled", "/sampled", "GET", 0.5)
	emitted, omitted := 0, 0
	for range 64 {
		expected := prediction.Float64() <= p.SampleRate
		if evaluator.Sample(&p) != expected {
			t.Fatal("legacy seeded sampling no longer replays predicted decisions")
		}
		if expected {
			emitted++
		} else {
			omitted++
		}
	}
	if emitted == 0 || omitted == 0 {
		t.Fatal("characterization must include both emission and omission")
	}
}
