// fips-mode-probe characterizes selected crypto paths. It does not certify an
// application, deployment, host, password scheme or backup encryption stack.
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/fips140"
	"crypto/md5"
	"crypto/rand"
	"encoding/json"
	"flag"
	"os"
	"runtime"
	"runtime/debug"

	"golang.org/x/crypto/argon2"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

type result struct {
	Case          string            `json:"case"`
	Status        string            `json:"status"`
	Enabled       bool              `json:"module_enabled"`
	ModuleVersion string            `json:"module_version"`
	GoVersion     string            `json:"go_version"`
	GOOS          string            `json:"goos"`
	GOARCH        string            `json:"goarch"`
	BuildSettings map[string]string `json:"build_settings"`
	Scope         string            `json:"scope"`
}

func main() {
	name := flag.String("case", "identity", "identity, aes-gcm, fernet, bcrypt, argon2, or md5 negative control")
	flag.Parse()
	r := result{Case: *name, Enabled: fips140.Enabled(), ModuleVersion: fips140.Version(),
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		BuildSettings: map[string]string{}, Scope: "local mode/path characterization only; no application or operating-environment validation"}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, item := range info.Settings {
			if item.Key == "GOFIPS140" || item.Key == "GOOS" || item.Key == "GOARCH" || item.Key == "vcs.revision" || item.Key == "vcs.modified" {
				r.BuildSettings[item.Key] = item.Value
			}
		}
	}
	r.Status = characterize(*name)
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		os.Exit(1)
	}
	if r.Status == "unknown_case" {
		os.Exit(2)
	}
}

func characterize(name string) (status string) {
	// Only a fixed result is emitted: synthetic keys, hashes, errors and tokens
	// are never included. A rejected path is evidence, not an ignored failure.
	defer func() {
		if recover() != nil {
			status = "rejected_panic"
		}
	}()
	switch name {
	case "identity":
		return "reported"
	case "aes-gcm":
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return "rejected_error"
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return "rejected_error"
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return "rejected_error"
		}
		sealed := aead.Seal(nil, nil, []byte("synthetic"), nil)
		opened, err := aead.Open(nil, nil, sealed, nil)
		if err != nil || string(opened) != "synthetic" {
			return "rejected_error"
		}
		return "completed"
	case "fernet":
		key, err := auth.GenerateKey()
		if err != nil {
			return "rejected_error"
		}
		enc, err := auth.NewEncryptor(key)
		if err != nil {
			return "rejected_error"
		}
		sealed, err := enc.Encrypt("synthetic")
		if err != nil || !enc.IsPrimaryCiphertext(sealed) {
			return "rejected_error"
		}
		opened, err := enc.Decrypt(sealed)
		if err != nil || opened != "synthetic" {
			return "rejected_error"
		}
		return "completed"
	case "bcrypt":
		encoded, err := auth.HashPassword("synthetic-password")
		if err != nil {
			return "rejected_error"
		}
		ok, _, err := auth.VerifyPassword(encoded, "synthetic-password")
		if err != nil || !ok {
			return "rejected_error"
		}
		return "completed_outside_native_module"
	case "argon2":
		if len(argon2.IDKey([]byte("synthetic"), []byte("synthetic-salt"), 1, 64, 1, 32)) != 32 {
			return "rejected_error"
		}
		return "completed_outside_native_module"
	case "md5":
		_ = md5.Sum([]byte("synthetic-negative-control"))
		return "completed"
	default:
		return "unknown_case"
	}
}
