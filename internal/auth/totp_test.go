package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestGenerateSecret_RoundTripVerify(t *testing.T) {
	secret, url, err := GenerateSecret("alice@example.com", "Astronomer")
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	if secret == "" {
		t.Fatal("secret is empty")
	}
	if !strings.HasPrefix(url, "otpauth://totp/") {
		t.Fatalf("otpauth URL = %q, want otpauth://totp/...", url)
	}
	if !strings.Contains(url, "Astronomer") {
		t.Fatalf("otpauth URL %q must embed issuer", url)
	}

	// Round-trip: generate a code for "now" against the same secret and
	// confirm VerifyCode accepts it.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	ok, err := VerifyCode(secret, code)
	if err != nil {
		t.Fatalf("VerifyCode err: %v", err)
	}
	if !ok {
		t.Fatalf("VerifyCode(now) returned false, want true")
	}
}

func TestGenerateSecret_RejectsEmptyInputs(t *testing.T) {
	if _, _, err := GenerateSecret("", "Astronomer"); err == nil {
		t.Error("empty account name should error")
	}
	if _, _, err := GenerateSecret("alice", ""); err == nil {
		t.Error("empty issuer should error")
	}
}

func TestVerifyCode_AcceptsCurrentAndAdjacentWindow(t *testing.T) {
	secret, _, err := GenerateSecret("alice@example.com", "Astronomer")
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	now := time.Now()
	// Codes one window earlier / later should be accepted (skew=1).
	earlier, _ := totp.GenerateCodeCustom(secret, now.Add(-time.Duration(TOTPPeriod)*time.Second), totp.ValidateOpts{
		Period: TOTPPeriod, Digits: TOTPDigits, Algorithm: otp.AlgorithmSHA1,
	})
	later, _ := totp.GenerateCodeCustom(secret, now.Add(time.Duration(TOTPPeriod)*time.Second), totp.ValidateOpts{
		Period: TOTPPeriod, Digits: TOTPDigits, Algorithm: otp.AlgorithmSHA1,
	})
	for label, code := range map[string]string{"earlier": earlier, "later": later} {
		ok, err := VerifyCode(secret, code)
		if err != nil {
			t.Errorf("VerifyCode(%s) err: %v", label, err)
		}
		if !ok {
			t.Errorf("VerifyCode(%s) returned false, want true (skew=1 should accept ±1 window)", label)
		}
	}

	// Two windows out should be rejected.
	farFuture, _ := totp.GenerateCodeCustom(secret, now.Add(time.Duration(TOTPPeriod*3)*time.Second), totp.ValidateOpts{
		Period: TOTPPeriod, Digits: TOTPDigits, Algorithm: otp.AlgorithmSHA1,
	})
	ok, err := VerifyCode(secret, farFuture)
	if err != nil {
		t.Errorf("VerifyCode(farFuture) err: %v", err)
	}
	if ok {
		t.Errorf("VerifyCode(farFuture) returned true, want false (skew=1 should NOT accept ±3 windows)")
	}
}

func TestVerifyCode_RejectsMalformed(t *testing.T) {
	secret, _, err := GenerateSecret("alice@example.com", "Astronomer")
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	cases := []string{"", "  ", "abc", "12345", "1234567"}
	for _, c := range cases {
		ok, _ := VerifyCode(secret, c)
		if ok {
			t.Errorf("VerifyCode(secret, %q) = true, want false", c)
		}
	}
}

func TestVerifyCode_RejectsInvalidSecret(t *testing.T) {
	_, err := VerifyCode("", "123456")
	if err == nil {
		t.Error("VerifyCode with empty secret should return error")
	}
}

func TestRecoveryCodes_RoundTripHash(t *testing.T) {
	codes, hashes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != 10 || len(hashes) != 10 {
		t.Fatalf("expected 10 codes + 10 hashes, got %d / %d", len(codes), len(hashes))
	}
	for i, code := range codes {
		// Display form is XXXXX-XXXXX.
		if !strings.Contains(code, "-") {
			t.Errorf("code %d %q missing hyphen", i, code)
		}
		// Hash must match the stored hash even when the user pastes
		// without the hyphen and in lowercase.
		variants := []string{
			code,
			strings.ReplaceAll(code, "-", ""),
			strings.ToLower(code),
			strings.ToLower(strings.ReplaceAll(code, "-", "")),
			" " + code + " ",
		}
		for _, v := range variants {
			if got := HashRecoveryCode(v); got != hashes[i] {
				t.Errorf("code %d variant %q hashed differently:\n got = %s\nwant = %s", i, v, got, hashes[i])
			}
		}
	}
}

func TestRecoveryCodes_UniqueAcrossN(t *testing.T) {
	codes, _, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate recovery code: %s", c)
		}
		seen[c] = true
	}
}

// TestRecoveryCodes_DoubleUseRejected enforces the policy at the hash level:
// once a code has been "consumed", asking again for the same plaintext yields
// the same hash, but the DB layer's used_at gate (ConsumeRecoveryCode) is
// what actually stops the replay. This test pins the hash determinism that
// invariant relies on — if HashRecoveryCode ever started salting or randomising,
// the DB layer's idempotency would silently fail.
func TestRecoveryCodes_DoubleUseRejected(t *testing.T) {
	codes, hashes, err := GenerateRecoveryCodes(2)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	// The same input must hash identically every time — the
	// ConsumeRecoveryCode SQL UPDATE relies on this equality to find
	// the row it's marking used.
	for i := range codes {
		again := HashRecoveryCode(codes[i])
		if again != hashes[i] {
			t.Errorf("HashRecoveryCode is non-deterministic: %s vs %s", again, hashes[i])
		}
	}
	// Two distinct codes must NOT produce the same hash (50 bits of
	// entropy makes a collision astronomical, but we sanity-check).
	if hashes[0] == hashes[1] {
		t.Error("two distinct recovery codes hashed to the same value")
	}
}

func TestQRCodeDataURL_NonEmpty(t *testing.T) {
	_, url, err := GenerateSecret("alice@example.com", "Astronomer")
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	dataURL, err := QRCodeDataURL(url)
	if err != nil {
		t.Fatalf("QRCodeDataURL: %v", err)
	}
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Errorf("dataURL prefix wrong: %q", dataURL[:40])
	}
	if len(dataURL) < 1000 {
		t.Errorf("dataURL implausibly short: %d bytes", len(dataURL))
	}
}
