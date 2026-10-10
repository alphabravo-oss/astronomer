//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

const (
	namespaceTemplate = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}`
	releaseTemplate   = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{.type}}{{"\t"}}{{index .metadata.labels "owner"}}{{"\t"}}{{index .metadata.labels "name"}}{{"\t"}}{{index .metadata.labels "version"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}`
	podTemplate       = `{{.metadata.uid}}{{"\t"}}{{.metadata.name}}{{"\t"}}{{.metadata.namespace}}{{"\t"}}{{index .metadata.labels "astronomer.io/owner"}}{{"\t"}}{{if .metadata.deletionTimestamp}}deleted{{else}}live{{end}}`
)

// FenceOptions binds the original installed Helm revision Secret. Upgrading
// creates another revision Secret; that must not invalidate the original
// anchor while it remains in history. ReleaseUID is this original Secret UID,
// not the UID of whichever revision is currently latest. History pruning or
// removal fails the fence and requires assessment rather than re-admission.
type FenceOptions struct {
	Identity    upgrade.Identity
	Kubeconfig  string
	Revision    uint64
	DatabasePod string
}

// The concrete command boundary is kept typed without exposing private stdout
// to the state machine. execute is injected internally only by tests.
type OwnershipFence struct {
	options        FenceOptions
	kubectl        Tool
	commands       *Commands
	configIdentity os.FileInfo
	configHash     [32]byte
	execute        func(context.Context, Tool, []string) ([]byte, error)
}

var kubeName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)

func NewOwnershipFence(commands *Commands, kubectl Tool, options FenceOptions) (*OwnershipFence, error) {
	id := options.Identity
	endpoint, endpointErr := url.Parse(id.APIServer)
	if commands == nil || kubectl.Kind != Kubectl || options.Revision == 0 || !filepath.IsAbs(options.Kubeconfig) || !kubeName.MatchString(id.Namespace) || !kubeName.MatchString(id.Release) || id.NamespaceUID == "" || id.ReleaseUID == "" || id.Context == "" || strings.ContainsAny(id.Context, "\t\r\n") || id.APIServer == "" || (options.DatabasePod != "" && !kubeName.MatchString(options.DatabasePod)) {
		return nil, errors.New("exact immutable Kubernetes/release fence binding required")
	}
	if endpointErr != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("explicit safe HTTPS Kubernetes API identity required")
	}
	secret := "sh.helm.release.v1." + id.Release + ".v" + strconv.FormatUint(options.Revision, 10)
	if !kubeName.MatchString(secret) {
		return nil, errors.New("original Helm revision anchor name exceeds Kubernetes bound")
	}
	configIdentity, configHash, err := privateBoundInput(options.Kubeconfig)
	if err != nil {
		return nil, err
	}
	f := &OwnershipFence{options: options, kubectl: kubectl, commands: commands, configIdentity: configIdentity, configHash: configHash}
	f.execute = func(ctx context.Context, tool Tool, args []string) ([]byte, error) {
		raw, _, err := commands.Execute(ctx, tool, args)
		return raw, err
	}
	return f, nil
}

// Fence makes bounded, joined read-only observations immediately before a
// stage. The private input remains bound to its constructor identity and bytes
// across stages. This is a point-in-time check, not an atomic Kubernetes lock.
// The replacement adapter must additionally prove database/restore identities
// at the destructive SQL operation; this method alone never authorizes DROP.
func (f *OwnershipFence) Fence(ctx context.Context, identity upgrade.Identity, replacement *upgrade.DisposableLease) error {
	if ctx == nil || f == nil || identity != f.options.Identity {
		return errors.New("ownership fence binding differs")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	configIdentity, configHash, err := privateBoundInput(f.options.Kubeconfig)
	if err != nil {
		return err
	}
	if !os.SameFile(f.configIdentity, configIdentity) || f.configHash != configHash {
		return errors.New("private kubeconfig differs from initial ownership binding")
	}
	base := []string{"--kubeconfig", f.options.Kubeconfig, "--context", identity.Context}
	read := func(args ...string) ([]byte, error) {
		return f.execute(ctx, f.kubectl, append(append([]string(nil), base...), args...))
	}
	server, err := read("config", "view", "--minify", "--raw", "--output", "jsonpath={.clusters[0].cluster.server}")
	if err != nil {
		return err
	}
	if string(server) != identity.APIServer {
		return errors.New("explicit Kubernetes API identity differs")
	}
	namespace, err := read("get", "namespace", identity.Namespace, "--output", "go-template="+namespaceTemplate)
	if err != nil {
		return err
	}
	if !equalFields(namespace, identity.NamespaceUID, identity.Namespace, "live") {
		return errors.New("namespace immutable identity or lifecycle differs")
	}
	secretName := "sh.helm.release.v1." + identity.Release + ".v" + strconv.FormatUint(f.options.Revision, 10)
	release, err := read("get", "secret", secretName, "--namespace", identity.Namespace, "--output", "go-template="+releaseTemplate)
	if err != nil {
		return err
	}
	if !equalFields(release, identity.ReleaseUID, secretName, identity.Namespace, "helm.sh/release.v1", "helm", identity.Release, strconv.FormatUint(f.options.Revision, 10), "live") {
		return errors.New("original Helm release anchor identity differs")
	}
	if replacement != nil {
		if replacement.NamespaceUID != identity.NamespaceUID || replacement.DatabasePodUID == "" || replacement.Owner == "" || f.options.DatabasePod == "" || replacement.DatabaseName == "" || replacement.RestoreDatabaseName == "" || replacement.DatabaseName == replacement.RestoreDatabaseName {
			return errors.New("explicit disposable database lease differs")
		}
		pod, err := read("get", "pod", f.options.DatabasePod, "--namespace", identity.Namespace, "--output", "go-template="+podTemplate)
		if err != nil {
			return err
		}
		if !equalFields(pod, replacement.DatabasePodUID, f.options.DatabasePod, identity.Namespace, replacement.Owner, "live") {
			return errors.New("disposable database pod identity or owner differs")
		}
	}
	currentIdentity, currentHash, err := privateBoundInput(f.options.Kubeconfig)
	if err != nil {
		return err
	}
	if !os.SameFile(configIdentity, currentIdentity) || configHash != currentHash {
		return errors.New("private kubeconfig binding changed during ownership observation")
	}
	return ctx.Err()
}

func equalFields(raw []byte, fields ...string) bool {
	// Kubectl templates emit no newline. Reject delimiter/control characters in
	// expected and observed fields rather than normalizing unknown output.
	for _, field := range fields {
		if field == "" || strings.ContainsAny(field, "\t\r\n") {
			return false
		}
	}
	return string(raw) == strings.Join(fields, "\t")
}

func privateBoundInput(path string) (os.FileInfo, [32]byte, error) {
	var empty [32]byte
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Size() > 1<<20 {
		return nil, empty, errors.New("private bounded regular operation input reference required")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, empty, errors.New("open private operation input reference")
	}
	file := os.NewFile(uintptr(fd), "private-kubeconfig")
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(before, actual) || !actual.Mode().IsRegular() || actual.Mode().Perm() != 0o600 || actual.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, empty, errors.New("private operation input owner or identity differs")
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, (1<<20)+1))
	if err != nil || count > 1<<20 {
		return nil, empty, errors.New("private operation input byte bound exceeded")
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return actual, sum, nil
}
