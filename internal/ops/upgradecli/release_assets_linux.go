//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// Acquisition acknowledges bounded bytes only. The concrete invocation still
// verifies release metadata, exact chart/OCI equality, attestations, checksums
// and every first-party image before any Driver mutation. Operator repository
// selection does not change those independent fixed signing policies.
const releaseAcquisitionMaximum = int64(256 << 20)
const releaseAssetMetadataMaximum = int64(1 << 20)
const releaseAssetContentMaximum = int64(64 << 20)

var releaseRecoveryAssetName = regexp.MustCompile(`^release-recovery[A-Za-z0-9._-]{0,128}\.json$`)

const releaseAssetProjection = `{tag_name:.tag_name,draft:.draft,prerelease:.prerelease,assets:[.assets[]|{id:.id,name:.name,size:.size,state:.state}]}`

type acquiredRelease struct {
	Tag        string                 `json:"tag_name"`
	Draft      *bool                  `json:"draft"`
	Prerelease *bool                  `json:"prerelease"`
	Assets     []acquiredReleaseAsset `json:"assets"`
}
type acquiredReleaseAsset struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

func selectedReleaseAssets(raw []byte, target string) ([]acquiredReleaseAsset, error) {
	if !legacyStableTarget.MatchString(target) || len(raw) == 0 || int64(len(raw)) > 16<<20 {
		return nil, errors.New("bounded exact release selection required")
	}
	var record acquiredRelease
	if jsoncontract.Decode(raw, &record) != nil || record.Tag != target || record.Draft == nil || *record.Draft || record.Prerelease == nil || *record.Prerelease || len(record.Assets) > 4096 {
		return nil, errors.New("release asset selection contract differs")
	}
	chart := "astronomer-" + strings.TrimPrefix(target, "v") + ".tgz"
	mandatory := map[string]bool{"RELEASE_IMAGES": false, "SHA256SUMS": false, chart: false, "release-manifest.json": false, "release-manifest.sigstore.json": false}
	seen := make(map[string]bool)
	ids := make(map[int64]bool)
	chosen := make([]acquiredReleaseAsset, 0, 7)
	total := int64(0)
	for _, asset := range record.Assets {
		if asset.ID <= 0 || ids[asset.ID] || asset.Name == "" || seen[asset.Name] {
			return nil, errors.New("ambiguous release asset identity")
		}
		ids[asset.ID] = true
		seen[asset.Name] = true
		_, required := mandatory[asset.Name]
		if !required && !releaseRecoveryAssetName.MatchString(asset.Name) {
			continue
		}
		maximum := releaseAssetMetadataMaximum
		if asset.Name == chart {
			maximum = releaseAssetContentMaximum
		}
		if asset.Name == "RELEASE_IMAGES" || asset.Name == "SHA256SUMS" {
			maximum = releaseSmallFilesMaximum
		}
		if asset.State != "uploaded" || asset.Size <= 0 || asset.Size > maximum || asset.Size > releaseAcquisitionMaximum-total {
			return nil, errors.New("release asset size/state exceeds admission")
		}
		total += asset.Size
		chosen = append(chosen, asset)
		if required {
			mandatory[asset.Name] = true
		}
	}
	for _, present := range mandatory {
		if !present {
			return nil, errors.New("required release asset is missing")
		}
	}
	if seen["release-recovery.json"] != seen["release-recovery.sigstore.json"] {
		return nil, errors.New("incomplete release recovery pair")
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Name < chosen[j].Name })
	return chosen, nil
}

// AcquireReleaseAssets preserves the network default through the bound native
// GH API client and explicit named operator credentials. It projects only a
// closed bounded metadata record, then streams each exact asset ID through the
// owned process stdout artifact limit. The untrusted size can bound allocation
// but never authenticate a release. Originals and partial captures survive.
func AcquireReleaseAssets(ctx context.Context, commands *Commands, github Tool, selection LegacyEnvironment, target, directory string) error {
	if ctx == nil || commands == nil || github.Kind != GitHubCLI || !legacyStableTarget.MatchString(target) || !releaseRepositoryName.MatchString(selection.ReleaseRepository) {
		return errors.New("explicit release acquisition composition required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := privateBootstrapDirectory(directory); err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		return errors.New("exclusive empty release asset scope required")
	}
	if selection.ReleaseArtifactDirectory != "" {
		return copyOfflineReleaseAssets(ctx, selection.ReleaseArtifactDirectory, target, directory)
	}
	endpoint := "repos/" + selection.ReleaseRepository + "/releases/tags/" + target
	raw, _, err := commands.Execute(ctx, github, []string{"api", endpoint, "--method", "GET", "--jq", releaseAssetProjection})
	if err != nil {
		return err
	}
	assets, err := selectedReleaseAssets(raw, target)
	if err != nil {
		return err
	}
	total := int64(0)
	for _, asset := range assets {
		total += asset.Size
	}
	// Account for both retained stdout originals and copied named release inputs.
	if err := artifactCapacity(directory, 2*total); err != nil {
		return err
	}
	for _, asset := range assets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := artifactCapacity(directory, 2*asset.Size); err != nil {
			return err
		}
		endpoint := "repos/" + selection.ReleaseRepository + "/releases/assets/" + strconv.FormatInt(asset.ID, 10)
		_, result, err := commands.executeWithOutput(ctx, github, []string{"api", endpoint, "--method", "GET", "--header", "Accept: application/octet-stream"}, process.ArtifactOutput{MaximumBytes: asset.Size})
		if err != nil {
			return err
		}
		capture := result.StdoutArtifact
		if capture == nil || capture.SizeBytes != asset.Size || !capture.Acknowledged {
			return errors.New("release asset did not acknowledge exact bounded bytes")
		}
		body, err := readCapture(commands.options.CaptureDirectory, filepath.Base(capture.Path), asset.Size, capture.SHA256)
		if err != nil {
			return err
		}
		if err := writeBootstrapBytes(ctx, directory, asset.Name, body, 0600); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Offline public release-kit inputs retain their original permissions and
// names. Only regular current-user 0600/0644 single-link files are admitted;
// exact opened/named identity and bytes are rechecked around each copy. The
// owned output becomes private0600, independently of operator source layout.
type offlineReleaseInput struct {
	path    string
	maximum int64
	bound   metadataInput
}

func readOfflineReleaseInput(ctx context.Context, path string, maximum int64) ([]byte, offlineReleaseInput, error) {
	var empty offlineReleaseInput
	if ctx == nil || !filepath.IsAbs(path) || maximum <= 0 {
		return nil, empty, errors.New("exact bounded offline release input required")
	}
	if err := ctx.Err(); err != nil {
		return nil, empty, err
	}
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return nil, empty, errors.New("offline release directory alias forbidden")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || (before.Mode().Perm() != 0600 && before.Mode().Perm() != 0644) || before.Size() > maximum || before.Sys().(*syscall.Stat_t).Uid != safeconv.Uint32(os.Getuid()) || before.Sys().(*syscall.Stat_t).Nlink != 1 {
		return nil, empty, errors.New("offline release source mode/owner/budget differs")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, empty, errors.New("open offline release source")
	}
	file := os.NewFile(uintptr(fd), "offline-release-source")
	defer func(close func() error) { _ = close() }(file.Close)
	opened, err := file.Stat()
	if err != nil || !sameOfflineReleaseInput(before, opened) {
		return nil, empty, errors.New("opened offline release identity differs")
	}
	raw := make([]byte, 0, before.Size())
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, empty, err
		}
		n, readErr := file.Read(buffer)
		if int64(n) > maximum-int64(len(raw)) {
			return nil, empty, errors.New("offline release source exceeds bound")
		}
		raw = append(raw, buffer[:n]...)
		if readErr == io.EOF {
			break
		}
		if readErr != nil || n == 0 {
			return nil, empty, errors.New("read offline release source")
		}
	}
	after, err := file.Stat()
	named, namedErr := os.Lstat(path)
	if err != nil || namedErr != nil || !sameOfflineReleaseInput(before, after) || !sameOfflineReleaseInput(before, named) || int64(len(raw)) != before.Size() {
		return nil, empty, errors.New("offline release source changed while reading")
	}
	input := offlineReleaseInput{path, maximum, metadataInput{before, sha256.Sum256(raw)}}
	return raw, input, ctx.Err()
}
func sameOfflineReleaseInput(before, after os.FileInfo) bool {
	if before == nil || after == nil {
		return false
	}
	mode := before.Mode().Perm()
	return (mode == 0600 || mode == 0644) && before.Mode().IsRegular() && after.Mode().IsRegular() && before.Mode() == after.Mode() && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && before.Sys().(*syscall.Stat_t).Uid == safeconv.Uint32(os.Getuid()) && after.Sys().(*syscall.Stat_t).Uid == safeconv.Uint32(os.Getuid()) && before.Sys().(*syscall.Stat_t).Nlink == 1 && after.Sys().(*syscall.Stat_t).Nlink == 1
}
func recheckOfflineReleaseInputs(ctx context.Context, inputs []offlineReleaseInput) error {
	for _, initial := range inputs {
		_, after, err := readOfflineReleaseInput(ctx, initial.path, initial.maximum)
		if err != nil {
			return err
		}
		if !sameOfflineReleaseInput(initial.bound.identity, after.bound.identity) || initial.bound.hash != after.bound.hash {
			return errors.New("initial offline release source changed")
		}
	}
	return ctx.Err()
}

func copyOfflineReleaseAssets(ctx context.Context, source, target, directory string) error {
	if source == directory || !filepath.IsAbs(source) {
		return errors.New("distinct absolute offline release input scope required")
	}
	chart := "astronomer-" + strings.TrimPrefix(target, "v") + ".tgz"
	names := []string{chart, "release-manifest.json", "release-manifest.sigstore.json", "release-recovery.json", "release-recovery.sigstore.json", "RELEASE_IMAGES"}
	inputs := make([]offlineReleaseInput, 0, 14)
	bodies := make(map[string][]byte)
	total := int64(0)
	read := func(name string, maximum int64, required bool) ([]byte, error) {
		path := filepath.Join(source, name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) && !required {
			return nil, nil
		}
		raw, input, err := readOfflineReleaseInput(ctx, path, maximum)
		if err != nil {
			return nil, err
		}
		if int64(len(raw)) > releaseAcquisitionMaximum-total {
			return nil, errors.New("offline release total budget exceeded")
		}
		total += int64(len(raw))
		inputs = append(inputs, input)
		return raw, nil
	}
	for _, name := range names {
		maximum := releaseAssetMetadataMaximum
		if name == chart {
			maximum = releaseAssetContentMaximum
		}
		if name == "RELEASE_IMAGES" {
			maximum = releaseSmallFilesMaximum
		}
		raw, err := read(name, maximum, name == chart || name == "release-manifest.json" || name == "release-manifest.sigstore.json")
		if err != nil {
			return err
		}
		if raw != nil {
			bodies[name] = raw
		}
	}
	if (bodies["release-recovery.json"] == nil) != (bodies["release-recovery.sigstore.json"] == nil) {
		return errors.New("incomplete offline release recovery pair")
	}
	if bodies["RELEASE_IMAGES"] == nil {
		// Decode for inventory shape only. No signed metadata/Driver authority is
		// claimed; the full invocation will verify exactly these copied bytes.
		var manifest releasecontract.Manifest
		if jsoncontract.Decode(bodies["release-manifest.json"], &manifest) != nil {
			return errors.New("offline release inventory manifest is invalid")
		}
		if _, err := manifest.Validate(target); err != nil {
			return errors.New("offline release inventory shape differs")
		}
		refs := make([]string, 0, len(manifest.Astronomer.Images))
		for _, image := range manifest.Astronomer.Images {
			raw, err := read(image.Name+".digest", releaseSmallFilesMaximum, true)
			if err != nil {
				return err
			}
			first, _, _ := strings.Cut(string(raw), "\n")
			if first == "" || strings.ContainsAny(first, "\r\t ") {
				return errors.New("offline digest first line invalid")
			}
			refs = append(refs, first)
		}
		sort.Strings(refs)
		bodies["RELEASE_IMAGES"] = []byte(strings.Join(refs, "\n") + "\n")
	}
	sums := make([]string, 0, 4)
	for _, name := range []string{"RELEASE_IMAGES", chart, "release-manifest.json", "release-manifest.sigstore.json"} {
		hash := sha256.Sum256(bodies[name])
		sums = append(sums, hex.EncodeToString(hash[:])+"  "+name)
	}
	bodies["SHA256SUMS"] = []byte(strings.Join(sums, "\n") + "\n")
	if err := recheckOfflineReleaseInputs(ctx, inputs); err != nil {
		return err
	}
	if err := artifactCapacity(directory, total+releaseSmallFilesMaximum); err != nil {
		return err
	}
	for _, name := range append(names, "SHA256SUMS") {
		raw, present := bodies[name]
		if !present {
			continue
		}
		if err := writeBootstrapBytes(ctx, directory, name, raw, 0600); err != nil {
			return err
		}
	}
	return recheckOfflineReleaseInputs(ctx, inputs)
}
