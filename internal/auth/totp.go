// Package auth — TOTP (RFC 6238) helpers for the local-password 2FA flow.
//
// We use the standard pquerna/otp implementation with the universal-default
// parameters: SHA-1 HMAC, 30-second window, 6-digit codes. These match
// every consumer authenticator app (Google Authenticator, Authy, 1Password,
// Bitwarden, etc.) so users can pick whichever they already trust.
//
// Secrets themselves never live in this package after enrollment — the
// caller wraps them through auth.Encryptor so the at-rest copy is
// Fernet-encrypted. The plaintext only re-materialises inside VerifyCode
// for the duration of a single HMAC.
package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTPPeriod is the RFC 6238 default — 30 seconds. Hard-coded because
// every consumer authenticator app assumes it and surfacing it as a knob
// would only invite misconfiguration.
const TOTPPeriod = 30

// TOTPSkew is how many ±period windows we accept on verify. 1 step on
// each side covers normal client-clock drift (NTP error, phone slow by a
// few seconds) without materially widening the brute-force horizon. RFC
// 6238 §5.2 explicitly allows this.
const TOTPSkew uint = 1

// TOTPDigits is the code length. 6 is the universal default; 7/8 are
// supported by the spec but break consumer apps.
const TOTPDigits = otp.DigitsSix

// TOTPSecretBytes is the size of the freshly-generated shared secret in
// bytes. 20 bytes = 160 bits = the RFC 4226 §4 recommendation; the
// resulting base32 string is 32 characters which is what Google
// Authenticator's manual-entry box was sized for.
const TOTPSecretBytes uint = 20

// RecoveryCodeCount is the number of recovery codes generated per
// enrollment / regen. 10 codes is the Github / Stripe / Cloudflare
// convention and the number the user can realistically write down.
const RecoveryCodeCount = 10

// recoveryCodeAlphabet excludes 0/O/1/I/L so a user hand-copying from
// the displayed sheet doesn't trip over visually-ambiguous glyphs. 32
// symbols = 5 bits each; with 10 displayed characters that's 50 bits
// of entropy per code — well above the threshold needed for the codes
// to be unguessable, especially since the table has the per-code
// unique index and we rate-limit verify attempts.
const recoveryCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// recoveryCodeRawLen is the un-formatted length the user pastes back to
// us. Display is XXXXX-XXXXX (split by a hyphen for readability), but
// the hyphen is stripped on verify so users can paste either form.
const recoveryCodeRawLen = 10

// ErrTOTPSecretInvalid is returned by VerifyCode when the stored secret
// fails base32-decoding. Should never happen for secrets produced by
// GenerateSecret, but defends against a corrupt DB row.
var ErrTOTPSecretInvalid = errors.New("totp: stored secret is not valid base32")

// GenerateSecret creates a fresh 160-bit secret + the otpauth:// URL the
// authenticator app encodes into the QR code. The plaintext secret is
// returned to the caller, which must wrap it via Encryptor.Encrypt
// before persisting; the URL is safe to return to the browser because
// the secret it embeds is already destined for the user's authenticator.
//
//   - accountName: shown under the account row in the user's
//     authenticator, e.g. "alice@astronomer.example.com". Convention is
//     username + '@' + issuer so two installs are easy to tell apart.
//   - issuer: the display name of the platform, e.g. "Astronomer". Comes
//     from the chart's auth.totp.issuer value so operators running
//     multiple installs (prod / staging) can disambiguate.
//
// SHA1 + 30s + 6 digits are the universal-app defaults; we don't expose
// them as parameters to keep enrollment foolproof.
func GenerateSecret(accountName, issuer string) (secret string, otpauthURL string, err error) {
	if strings.TrimSpace(accountName) == "" {
		return "", "", errors.New("totp: account name is required")
	}
	if strings.TrimSpace(issuer) == "" {
		return "", "", errors.New("totp: issuer is required")
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      TOTPPeriod,
		SecretSize:  TOTPSecretBytes,
		Digits:      TOTPDigits,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", fmt.Errorf("totp: generate: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// QRCodePNG renders the otpauth URL into a PNG byte slice suitable for
// returning to the browser as a data URL. Kept here (vs. handler) so
// the handler doesn't need to reach into a 3rd-party QR library —
// pquerna/otp wraps boombuler/barcode for us.
//
// The image is 512x512 so it stays crisp when displayed larger (easier to
// scan on dense/retina screens) while still tiny on the wire (~3-5kB).
func QRCodePNG(otpauthURL string) ([]byte, error) {
	key, err := otp.NewKeyFromURL(otpauthURL)
	if err != nil {
		return nil, fmt.Errorf("totp: parse otpauth url: %w", err)
	}
	img, err := key.Image(512, 512)
	if err != nil {
		return nil, fmt.Errorf("totp: encode qr: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("totp: encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// QRCodeDataURL is the convenience wrapper that returns the QR PNG as a
// "data:image/png;base64,..." string — the frontend can drop that
// straight into an <img src=...> without an extra round-trip.
func QRCodeDataURL(otpauthURL string) (string, error) {
	png, err := QRCodePNG(otpauthURL)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// VerifyCode validates a 6-digit code against the secret using the
// ±TOTPSkew period window. Returns false (NOT an error) for a
// well-formed-but-wrong code so the caller can short-circuit on
// "invalid_credentials" without a special-case branch.
//
// The single error case is "the stored secret isn't decodable" — that's
// a server-side bug (corrupted row or wrong-key decrypt) the caller
// must propagate, NOT a user-facing 401.
func VerifyCode(secret, code string) (bool, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return false, ErrTOTPSecretInvalid
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return false, nil
	}
	ok, err := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    TOTPPeriod,
		Skew:      TOTPSkew,
		Digits:    TOTPDigits,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		// Library returns ErrValidateSecretInvalidBase32 when the secret
		// is junk; surface that as ErrTOTPSecretInvalid (a stable
		// sentinel the caller can identify).
		return false, ErrTOTPSecretInvalid
	}
	return ok, nil
}

// GenerateRecoveryCodes returns N recovery codes + their sha256 hashes.
// The codes are returned to the caller exactly once — they're the
// plaintext value the user will see in the UI; only the hash is meant
// to ever land in the DB.
//
// Each code is 10 chars from a 32-char unambiguous alphabet (~50 bits
// of entropy). The user-facing form is XXXXX-XXXXX with a hyphen for
// readability; HashRecoveryCode normalises the input so a user pasting
// either form matches.
func GenerateRecoveryCodes(n int) (codes []string, hashes []string, err error) {
	if n <= 0 {
		n = RecoveryCodeCount
	}
	codes = make([]string, 0, n)
	hashes = make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw, err := randomFromAlphabet(recoveryCodeAlphabet, recoveryCodeRawLen)
		if err != nil {
			return nil, nil, fmt.Errorf("totp: generate recovery code: %w", err)
		}
		// Display form: first 5 + '-' + last 5. The hyphen is purely
		// presentational — the hash is over the un-hyphenated raw form
		// so user input either way matches.
		display := raw[:5] + "-" + raw[5:]
		codes = append(codes, display)
		hashes = append(hashes, HashRecoveryCode(display))
	}
	return codes, hashes, nil
}

// HashRecoveryCode returns hex(sha256(normalised(code))) — the
// verification-side hash. Input normalisation strips whitespace and
// hyphens and upper-cases the rest so users pasting "abcde-12345" or
// "ABCDE12345" match against the same stored hash.
//
// We use sha256 (NOT bcrypt) because recovery codes already carry ~50
// bits of entropy and are single-use — a slow KDF buys nothing the
// alphabet entropy doesn't already give us, and the verify path is on
// the login hot path.
func HashRecoveryCode(code string) string {
	normalised := strings.ToUpper(strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '-', '_':
			return -1
		}
		return r
	}, code))
	sum := sha256.Sum256([]byte(normalised))
	return hex.EncodeToString(sum[:])
}

// randomFromAlphabet samples `length` characters uniformly from the
// supplied alphabet using crypto/rand. We reject-sample so the modulo
// bias of a naive `int(b) % len(alphabet)` doesn't sneak a 0.4%
// distribution skew into the recovery codes (32 doesn't divide 256).
func randomFromAlphabet(alphabet string, length int) (string, error) {
	if length <= 0 {
		return "", errors.New("length must be positive")
	}
	if len(alphabet) == 0 || len(alphabet) > 256 {
		return "", errors.New("alphabet size must be in (0,256]")
	}
	max := byte(255 - (255 % len(alphabet)))
	out := make([]byte, 0, length)
	buf := make([]byte, length*2)
	for len(out) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b > max {
				continue
			}
			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == length {
				break
			}
		}
	}
	return string(out), nil
}
