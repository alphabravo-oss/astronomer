package auth

import (
	"context"
	"fmt"
	"unicode"
)

// PasswordPolicy is the local-account password rules (DIR-04). Defaults match
// platform_settings registry defaults.
type PasswordPolicy struct {
	MinLength        int
	RequireUppercase bool
	RequireLowercase bool
	RequireDigit     bool
	RequireSpecial   bool
}

// DefaultPasswordPolicy is used when platform settings are unavailable.
func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{
		MinLength:        12,
		RequireUppercase: true,
		RequireLowercase: true,
		RequireDigit:     true,
		RequireSpecial:   false,
	}
}

// PasswordPolicySettings is the minimal surface for reading password.*
// platform settings. *handler.SettingsCache satisfies this interface.
type PasswordPolicySettings interface {
	IntValue(ctx context.Context, key string, fallback int) int
	BoolValue(ctx context.Context, key string, fallback bool) bool
}

// LoadPasswordPolicy reads password.* keys from settings when available,
// falling back to DefaultPasswordPolicy for missing/unwired providers
// (AUTH-R01). Keys match the platform_settings registry:
// password.min_length, password.require_uppercase/lowercase/digit/special.
func LoadPasswordPolicy(ctx context.Context, settings PasswordPolicySettings) PasswordPolicy {
	p := DefaultPasswordPolicy()
	if settings == nil {
		return p
	}
	p.MinLength = settings.IntValue(ctx, "password.min_length", p.MinLength)
	p.RequireUppercase = settings.BoolValue(ctx, "password.require_uppercase", p.RequireUppercase)
	p.RequireLowercase = settings.BoolValue(ctx, "password.require_lowercase", p.RequireLowercase)
	p.RequireDigit = settings.BoolValue(ctx, "password.require_digit", p.RequireDigit)
	p.RequireSpecial = settings.BoolValue(ctx, "password.require_special", p.RequireSpecial)
	if p.MinLength < 1 {
		p.MinLength = DefaultPasswordPolicy().MinLength
	}
	return p
}

// ValidatePassword reports whether password satisfies the policy.
func ValidatePassword(password string, p PasswordPolicy) error {
	if p.MinLength < 1 {
		p.MinLength = 12
	}
	if len(password) < p.MinLength {
		return fmt.Errorf("password must be at least %d characters", p.MinLength)
	}
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}
	if p.RequireUppercase && !hasUpper {
		return fmt.Errorf("password must contain at least one uppercase letter")
	}
	if p.RequireLowercase && !hasLower {
		return fmt.Errorf("password must contain at least one lowercase letter")
	}
	if p.RequireDigit && !hasDigit {
		return fmt.Errorf("password must contain at least one digit")
	}
	if p.RequireSpecial && !hasSpecial {
		return fmt.Errorf("password must contain at least one special character")
	}
	return nil
}
