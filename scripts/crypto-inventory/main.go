// crypto-inventory records literal first-party Go crypto imports with source
// hashes. It is a source denominator, not a transitive call graph or validation.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type source struct {
	Path    string   `json:"path"`
	SHA256  string   `json:"sha256"`
	Imports []string `json:"imports"`
	Role    string   `json:"role"`
}

func main() {
	root := flag.String("root", ".", "clean source checkout")
	flag.Parse()
	result, err := inventory(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "crypto inventory: source parsing or identity failed")
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}

func git(root string, args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Dir = root
	return command.Output()
}

func inventory(root string) (map[string]any, error) {
	status, err := git(root, "status", "--porcelain")
	if err != nil || len(status) != 0 {
		return nil, fmt.Errorf("source is not frozen")
	}
	commit, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	paths, err := git(root, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	rows := []source{}
	count := 0
	for _, name := range strings.Split(string(paths), "\x00") {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		count++
		payload, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, payload, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		imports := []string{}
		for _, item := range parsed.Imports {
			path, err := strconv.Unquote(item.Path.Value)
			if err != nil {
				return nil, err
			}
			if cryptoImport(path) {
				imports = append(imports, path)
			}
		}
		if len(imports) == 0 {
			continue
		}
		sort.Strings(imports)
		digest := sha256.Sum256(payload)
		role := "application"
		if strings.HasSuffix(name, "_test.go") {
			role = "test"
		} else if strings.HasPrefix(name, "scripts/") || strings.HasPrefix(name, "tools/") {
			role = "engineering_or_build_tool"
		}
		rows = append(rows, source{name, hex.EncodeToString(digest[:]), imports, role})
	}
	return map[string]any{"schemaVersion": 1, "sourceCommit": strings.TrimSpace(string(commit)),
		"goSourceFilesParsed": count, "cryptoImportFiles": len(rows), "sources": rows,
		"scope": "All tracked Go files parsed with the Go AST; literal crypto/security-provider imports only. Separate third-party module closure, Python/OpenSSL, browser/NGINX and database boundaries require their own evidence."}, nil
}

func cryptoImport(path string) bool {
	for _, prefix := range []string{"crypto/", "golang.org/x/crypto/", "github.com/fernet/", "github.com/golang-jwt/", "github.com/go-jose/", "github.com/sigstore/", "github.com/pquerna/otp", "github.com/coreos/go-oidc/", "filippo.io/", "go.step.sm/crypto/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
