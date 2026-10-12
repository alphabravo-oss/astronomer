// fips-mode-probe characterizes selected crypto paths. It does not certify an
// application, deployment, host, password scheme or backup encryption stack.
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/fips140"
	"crypto/rand"
	"encoding/json"
	"flag"
	"os"
	"runtime"
	"runtime/debug"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"

	"github.com/fernet/fernet-go"
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
	name := flag.String("case", "identity", "identity, aes-gcm, fernet, bcrypt, argon2, or aes-gcm-external-nonce assessment control")
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
		key := new(fernet.Key)
		if err := key.Generate(); err != nil {
			return "rejected_error"
		}
		sealed, err := fernet.EncryptAndSign([]byte("synthetic"), key)
		if err != nil {
			return "rejected_error"
		}
		opened := fernet.VerifyAndDecrypt(sealed, 0, []*fernet.Key{key})
		if string(opened) != "synthetic" {
			return "rejected_error"
		}
		return "completed_library_path"
	case "bcrypt":
		encoded, err := bcrypt.GenerateFromPassword([]byte("synthetic-password"), bcrypt.DefaultCost)
		if err != nil {
			return "rejected_error"
		}
		if err := bcrypt.CompareHashAndPassword(encoded, []byte("synthetic-password")); err != nil {
			return "rejected_error"
		}
		return "completed_outside_native_module"
	case "argon2":
		if len(argon2.IDKey([]byte("synthetic"), []byte("synthetic-salt"), 1, 64, 1, 32)) != 32 {
			return "rejected_error"
		}
		return "completed_outside_native_module"
	case "aes-gcm-external-nonce":
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return "rejected_error"
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return "rejected_error"
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return "rejected_error"
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return "rejected_error"
		}
		_ = aead.Seal(nil, nonce, []byte("synthetic-assessment-control"), nil)
		return "completed"
	default:
		return "unknown_case"
	}
}
