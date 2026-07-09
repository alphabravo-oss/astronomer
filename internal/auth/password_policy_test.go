package auth

import (
	"context"
	"testing"
)

func TestValidatePassword_DefaultPolicy(t *testing.T) {
	p := DefaultPasswordPolicy()
	if err := ValidatePassword("Short1", p); err == nil {
		t.Fatal("expected min length failure")
	}
	if err := ValidatePassword("alllowercase1x", p); err == nil {
		t.Fatal("expected uppercase failure")
	}
	if err := ValidatePassword("ALLUPPERCASE1X", p); err == nil {
		t.Fatal("expected lowercase failure")
	}
	if err := ValidatePassword("NoDigitsHere!!", p); err == nil {
		t.Fatal("expected digit failure")
	}
	if err := ValidatePassword("ValidPassw0rd", p); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

// fakePasswordSettings implements PasswordPolicySettings for unit tests.
type fakePasswordSettings struct {
	ints  map[string]int
	bools map[string]bool
}

func (f *fakePasswordSettings) IntValue(_ context.Context, key string, fallback int) int {
	if f != nil && f.ints != nil {
		if v, ok := f.ints[key]; ok {
			return v
		}
	}
	return fallback
}

func (f *fakePasswordSettings) BoolValue(_ context.Context, key string, fallback bool) bool {
	if f != nil && f.bools != nil {
		if v, ok := f.bools[key]; ok {
			return v
		}
	}
	return fallback
}

// AUTH-R01: min_length override 16 must reject a 12-char password that
// would otherwise pass the default policy.
func TestLoadPasswordPolicy_MinLengthOverride(t *testing.T) {
	settings := &fakePasswordSettings{
		ints: map[string]int{"password.min_length": 16},
	}
	p := LoadPasswordPolicy(context.Background(), settings)
	if p.MinLength != 16 {
		t.Fatalf("MinLength = %d, want 16", p.MinLength)
	}
	// 12 chars with upper/lower/digit — passes default (min 12) but fails min 16.
	pw12 := "ValidPassw01" // 12
	if len(pw12) != 12 {
		t.Fatalf("test fixture length = %d, want 12", len(pw12))
	}
	if err := ValidatePassword(pw12, DefaultPasswordPolicy()); err != nil {
		t.Fatalf("default policy should accept fixture: %v", err)
	}
	if err := ValidatePassword(pw12, p); err == nil {
		t.Fatal("expected min_length=16 to reject 12-char password")
	}
}

func TestLoadPasswordPolicy_NilSettingsUsesDefault(t *testing.T) {
	p := LoadPasswordPolicy(context.Background(), nil)
	if p != DefaultPasswordPolicy() {
		t.Fatalf("got %+v, want default", p)
	}
}
