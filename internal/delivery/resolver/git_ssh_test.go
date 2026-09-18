package resolver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
)

func TestResolveGitSSHRejectsMissingPinnedMaterialBeforeDial(t *testing.T) {
	request := Request{
		Source:            model.Source{Type: model.SourceGit, URL: "ssh://git@git.corp.example/platform/config.git", AuthMode: model.AuthSSH},
		RequestedRevision: "main",
		Credential:        &CredentialMaterial{PrivateKey: []byte("key only")},
	}
	_, err := resolveGitSSH(context.Background(), request)
	if !HasCode(err, CodeAuthentication) || !strings.Contains(err.Error(), "known-hosts") {
		t.Fatalf("resolveGitSSH() error = %v, want missing known-hosts authentication failure", err)
	}
}

func TestParseSSHSignerRejectsMalformedKey(t *testing.T) {
	if _, err := parseSSHSigner([]byte("not a private key"), nil); err == nil {
		t.Fatal("parseSSHSigner accepted malformed private key")
	}
}

func TestKnownHostsCallbackPinsOriginalHostnameAndKey(t *testing.T) {
	trustedKey := newSSHSigner(t).PublicKey()
	untrustedKey := newSSHSigner(t).PublicKey()
	line := knownhosts.Line([]string{"[git.corp.example]:2222"}, trustedKey) + "\n"

	callback, cleanup, err := knownHostsCallback([]byte(line), "git.corp.example", 2222)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	remote := &net.TCPAddr{IP: net.ParseIP("10.42.1.7"), Port: 2222}

	// The transport connects to the policy-approved resolved IP, but trust must
	// remain pinned to the operator-configured hostname.
	if err := callback("10.42.1.7:2222", remote, trustedKey); err != nil {
		t.Fatalf("trusted host key was rejected: %v", err)
	}
	if err := callback("10.42.1.7:2222", remote, untrustedKey); err == nil || !strings.Contains(err.Error(), "did not match registered trust") {
		t.Fatalf("untrusted host key error = %v", err)
	}
}

func TestKnownHostsCallbackRejectsMalformedTrustMaterial(t *testing.T) {
	if _, cleanup, err := knownHostsCallback([]byte("not-known-hosts\n"), "git.corp.example", 22); err == nil {
		cleanup()
		t.Fatal("malformed known-hosts material was accepted")
	}
}

func TestBoundedSSHAuthCarriesPinnedTrustAndTimeout(t *testing.T) {
	signer := newSSHSigner(t)
	called := false
	timeout := 17 * time.Second
	auth := &boundedSSHAuth{
		user: "deploy", signer: signer, timeout: timeout,
		hostKey: func(string, net.Addr, xssh.PublicKey) error {
			called = true
			return nil
		},
	}
	config, err := auth.ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.User != "deploy" || config.Timeout != timeout || config.HostKeyCallback == nil || len(config.Auth) != 1 {
		t.Fatalf("unexpected SSH client config: %+v", config)
	}
	if err := config.HostKeyCallback("git.corp.example:22", &net.TCPAddr{}, signer.PublicKey()); err != nil || !called {
		t.Fatalf("host-key callback was not preserved: called=%v err=%v", called, err)
	}
}

func newSSHSigner(t *testing.T) xssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := xssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
