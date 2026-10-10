//go:build linux

package databasejob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/uuid"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sys/unix"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

func createWorkloadInput(ctx context.Context, root string, claim uuid.UUID, metadata resolver.WorkloadImageMetadata, subject, architecture string) (*WorkloadInput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Capture's closed parser has already checked every descriptor. Retain exact
	// originals, including private config, without interpreting application Env.
	selected := digest.Digest(subject)
	if selected.Validate() != nil || selected.Algorithm() != digest.SHA256 || len(metadata.Manifest) == 0 || len(metadata.Manifest) > 1<<20 || len(metadata.Config) == 0 || len(metadata.Config) > 4<<20 || metadata.ConfigDescriptor.Digest.Validate() != nil || metadata.ConfigDescriptor.Digest.Algorithm() != digest.SHA256 || metadata.ManifestDescriptor.Digest != selected || metadata.ManifestDescriptor.Size != int64(len(metadata.Manifest)) || metadata.ManifestDescriptor.MediaType != "application/vnd.oci.image.manifest.v1+json" && metadata.ManifestDescriptor.MediaType != "application/vnd.docker.distribution.manifest.v2+json" || len(metadata.Layers) > 32 || digest.FromBytes(metadata.Manifest).String() != subject || digest.FromBytes(metadata.Config) != metadata.ConfigDescriptor.Digest || int64(len(metadata.Config)) != metadata.ConfigDescriptor.Size {
		return nil, errors.New("workload metadata changed before private staging")
	}
	index, err := workloadIndexBytes(metadata, architecture)
	if err != nil || len(index) > 4<<10 {
		return nil, errors.New("workload index encoding invalid")
	}
	originals := []struct {
		name string
		data []byte
	}{{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)}, {"index.json", index}, {"blobs/sha256/" + digest.Digest(subject).Encoded(), metadata.Manifest}, {"blobs/sha256/" + metadata.ConfigDescriptor.Digest.Encoded(), metadata.Config}}
	snapshot := WorkloadInputSnapshot{ManifestDigest: subject, Architecture: architecture, IndexDigest: digest.FromBytes(index).String()}
	seen := map[string]bool{}
	for _, source := range originals {
		snapshot.Members = append(snapshot.Members, WorkloadInputMember{Name: source.name, Digest: digest.FromBytes(source.data).String(), Size: int64(len(source.data))})
		seen[source.name] = true
	}
	for _, layer := range metadata.Layers {
		if layer.Digest.Validate() != nil || layer.Digest.Algorithm() != digest.SHA256 || layer.Size <= 0 || layer.Size > 2<<30 {
			return nil, errors.New("workload original layer descriptor invalid")
		}
		name := "blobs/sha256/" + layer.Digest.Encoded()
		if !seen[name] {
			snapshot.Members = append(snapshot.Members, WorkloadInputMember{Name: name, Digest: layer.Digest.String(), Size: layer.Size})
			seen[name] = true
		}
	}
	if err := validateWorkloadInputSnapshot(snapshot); err != nil {
		return nil, err
	}
	var size int64
	for _, member := range snapshot.Members {
		size += member.Size
	}
	parent, err := openWorkloadRoot(root, size)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	name := "workload-" + claim.String()
	if unix.Mkdirat(int(parent.Fd()), name, 0700) != nil {
		return nil, errors.New("workload exclusive staging creation failed")
	}
	directory, err := openWorkloadDirectory(int(parent.Fd()), name, 0700)
	if err != nil {
		return nil, err
	}
	owner := &WorkloadInput{directory: filepath.Join(root, name), directoryFD: directory, snapshot: snapshot, files: make([]*os.File, len(snapshot.Members))}
	accepted := false
	defer func() {
		if !accepted {
			owner.Close()
		}
	}()
	if unix.Mkdirat(int(directory.Fd()), "blobs", 0700) != nil {
		return nil, errors.New("workload blobs directory creation failed")
	}
	owner.blobsFD, err = openWorkloadDirectory(int(directory.Fd()), "blobs", 0700)
	if err != nil {
		return nil, err
	}
	if unix.Mkdirat(int(owner.blobsFD.Fd()), "sha256", 0700) != nil {
		return nil, errors.New("workload algorithm directory creation failed")
	}
	owner.algorithmFD, err = openWorkloadDirectory(int(owner.blobsFD.Fd()), "sha256", 0700)
	if err != nil {
		return nil, err
	}
	for _, source := range originals {
		member := WorkloadInputMember{Name: source.name, Digest: digest.FromBytes(source.data).String(), Size: int64(len(source.data))}
		file, existing, err := owner.createBlob(member)
		if err != nil || existing {
			return nil, errors.New("workload original member creation failed")
		}
		copied, err := io.Copy(file, bytes.NewReader(source.data))
		if err != nil || copied != member.Size {
			return nil, errors.New("workload original member write incomplete")
		}
	}
	accepted = true
	return owner, nil
}

func (owner *WorkloadInput) createBlob(member WorkloadInputMember) (io.Writer, bool, error) {
	slot := -1
	for index, expected := range owner.snapshot.Members {
		if expected == member {
			slot = index
			break
		}
	}
	if slot < 0 {
		return nil, false, errors.New("workload member outside original closure")
	}
	if owner.files[slot] != nil {
		return owner.files[slot], true, nil
	}
	parent, name := owner.directoryFD, member.Name
	if name != "oci-layout" && name != "index.json" {
		parent = owner.algorithmFD
		name = digest.Digest(member.Digest).Encoded()
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, false, errors.New("workload literal member creation failed")
	}
	file := os.NewFile(uintptr(fd), "owned workload input member")
	if file == nil {
		_ = unix.Close(fd)
		return nil, false, errors.New("workload member handle unavailable")
	}
	owner.files[slot] = file
	if !workloadMemberOwner(fd, 0600) {
		return nil, false, errors.New("workload member ownership invalid")
	}
	return file, false, nil
}

func (owner *WorkloadInput) sealAndMeasure(ctx context.Context) error {
	for index, file := range owner.files {
		if file == nil || !workloadMemberOwner(int(file.Fd()), 0600) || file.Sync() != nil {
			return errors.New("workload complete member sync failed")
		}
		if err := owner.measureMember(ctx, index, 0600); err != nil {
			return err
		}
		if file.Chmod(0444) != nil {
			return errors.New("workload member seal failed")
		}
	}
	for _, directory := range []*os.File{owner.algorithmFD, owner.blobsFD, owner.directoryFD} {
		if directory.Chmod(0555) != nil || directory.Sync() != nil {
			return errors.New("workload directory seal failed")
		}
	}
	return owner.measure(ctx)
}

func reopenWorkloadInput(ctx context.Context, root string, claim uuid.UUID, expected WorkloadInputSnapshot) (*WorkloadInput, error) {
	parent, err := openWorkloadRoot(root, 1)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	name := "workload-" + claim.String()
	directory, err := openWorkloadDirectory(int(parent.Fd()), name, 0555)
	if err != nil {
		return nil, err
	}
	value := expected
	value.Members = append([]WorkloadInputMember(nil), expected.Members...)
	owner := &WorkloadInput{directory: filepath.Join(root, name), directoryFD: directory, snapshot: value, files: make([]*os.File, len(value.Members))}
	accepted := false
	defer func() {
		if !accepted {
			owner.Close()
		}
	}()
	owner.blobsFD, err = openWorkloadDirectory(int(directory.Fd()), "blobs", 0555)
	if err != nil {
		return nil, err
	}
	owner.algorithmFD, err = openWorkloadDirectory(int(owner.blobsFD.Fd()), "sha256", 0555)
	if err != nil {
		return nil, err
	}
	for index, member := range value.Members {
		parent, name := directory, member.Name
		if name != "oci-layout" && name != "index.json" {
			parent = owner.algorithmFD
			name = digest.Digest(member.Digest).Encoded()
		}
		fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, errors.New("workload sealed member unavailable")
		}
		owner.files[index] = os.NewFile(uintptr(fd), "owned sealed workload member")
		if owner.files[index] == nil {
			_ = unix.Close(fd)
			return nil, errors.New("workload sealed member handle unavailable")
		}
	}
	if _, err := owner.Measure(ctx); err != nil {
		return nil, err
	}
	accepted = true
	return owner, nil
}
func (owner *WorkloadInput) measure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := owner.validatePaths(0555); err != nil {
		return err
	}
	for index := range owner.files {
		if err := owner.measureMember(ctx, index, 0444); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := owner.inspectGraph(ctx); err != nil {
		return err
	}
	return owner.validatePaths(0555)
}
func (owner *WorkloadInput) measureMember(ctx context.Context, index int, mode uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	member, original := owner.snapshot.Members[index], owner.files[index]
	if original == nil || !workloadMemberOwner(int(original.Fd()), mode) {
		return errors.New("workload member physical owner changed")
	}
	parent, name := owner.directoryFD, member.Name
	if name != "oci-layout" && name != "index.json" {
		parent = owner.algorithmFD
		name = digest.Digest(member.Digest).Encoded()
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("workload member physical path unavailable")
	}
	opened := os.NewFile(uintptr(fd), "owned workload remeasurement")
	if opened == nil {
		_ = unix.Close(fd)
		return errors.New("workload measurement handle unavailable")
	}
	defer opened.Close()
	before, beforeErr := original.Stat()
	start, startErr := opened.Stat()
	if beforeErr != nil || startErr != nil || !workloadMemberOwner(fd, mode) || !os.SameFile(before, start) || before.Size() != member.Size || !before.ModTime().Equal(start.ModTime()) {
		return errors.New("workload member original identity changed")
	}
	hash := sha256.New()
	input := io.LimitReader(workloadContextReader{ctx: ctx, reader: opened}, member.Size+1)
	copied, err := io.CopyBuffer(hash, input, make([]byte, 64<<10))
	finish, statErr := opened.Stat()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || statErr != nil || copied != member.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != member.Digest || !os.SameFile(start, finish) || start.Size() != finish.Size() || !start.ModTime().Equal(finish.ModTime()) || !workloadMemberOwner(fd, mode) || !workloadMemberOwner(int(original.Fd()), mode) {
		return errors.New("workload complete bytes or sealed identity changed")
	}
	var current unix.Stat_t
	var pinned unix.Stat_t
	if unix.Fstatat(int(parent.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(fd, &pinned) != nil || current.Dev != pinned.Dev || current.Ino != pinned.Ino || current.Mode != pinned.Mode || current.Nlink != 1 || current.Uid != pinned.Uid {
		return errors.New("workload member replaced after complete read")
	}
	return nil
}

type workloadContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader workloadContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
func workloadMemberOwner(fd int, mode uint32) bool {
	var value unix.Stat_t
	return unix.Fstat(fd, &value) == nil && value.Mode&unix.S_IFMT == unix.S_IFREG && value.Mode&07777 == mode && int(value.Uid) == os.Geteuid() && value.Nlink == 1
}
func openWorkloadDirectory(parent int, name string, mode uint32) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("workload owned directory open failed")
	}
	var value unix.Stat_t
	if unix.Fstat(fd, &value) != nil || value.Mode&unix.S_IFMT != unix.S_IFDIR || value.Mode&07777 != mode || int(value.Uid) != os.Geteuid() {
		_ = unix.Close(fd)
		return nil, errors.New("workload directory mode/owner invalid")
	}
	return os.NewFile(uintptr(fd), "owned workload directory"), nil
}
func openWorkloadRoot(path string, size int64) (*os.File, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path || size <= 0 || size > maximumWorkloadInputBytes {
		return nil, errors.New("workload root or aggregate invalid")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() {
		return nil, errors.New("workload root unavailable")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("workload root open failed")
	}
	file := os.NewFile(uintptr(fd), "owned workload private root")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("workload root handle unavailable")
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = file.Close()
		}
	}()
	var value unix.Stat_t
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || unix.Fstat(fd, &value) != nil || value.Mode&unix.S_IFMT != unix.S_IFDIR || value.Mode&07777 != 0700 || int(value.Uid) != os.Geteuid() {
		return nil, errors.New("workload root identity/ownership changed")
	}
	var filesystem unix.Statfs_t
	if unix.Fstatfs(fd, &filesystem) != nil || filesystem.Bsize <= 0 || filesystem.Bavail > ^uint64(0)/uint64(filesystem.Bsize) || filesystem.Bavail*uint64(filesystem.Bsize) < uint64(size)+(2<<30) {
		return nil, errors.New("workload stage has insufficient finite reserve")
	}
	accepted = true
	return file, nil
}
func (owner *WorkloadInput) validatePaths(mode uint32) error {
	if owner.directoryFD == nil || owner.blobsFD == nil || owner.algorithmFD == nil {
		return errors.New("workload directories unavailable")
	}
	before, err := owner.directoryFD.Stat()
	actual, pathErr := os.Lstat(owner.directory)
	if err != nil || pathErr != nil || !os.SameFile(before, actual) {
		return errors.New("workload literal path replaced")
	}
	directories := []struct {
		file    *os.File
		parent  *os.File
		name    string
		members []string
	}{{owner.directoryFD, nil, "", []string{"blobs", "index.json", "oci-layout"}}, {owner.blobsFD, owner.directoryFD, "blobs", []string{"sha256"}}, {owner.algorithmFD, owner.blobsFD, "sha256", nil}}
	for _, member := range owner.snapshot.Members {
		if member.Name != "oci-layout" && member.Name != "index.json" {
			directories[2].members = append(directories[2].members, digest.Digest(member.Digest).Encoded())
		}
	}
	for _, entry := range directories {
		var pinned unix.Stat_t
		if unix.Fstat(int(entry.file.Fd()), &pinned) != nil || pinned.Mode&unix.S_IFMT != unix.S_IFDIR || pinned.Mode&07777 != mode || int(pinned.Uid) != os.Geteuid() {
			return errors.New("workload sealed directory mode/owner changed")
		}
		if entry.parent != nil {
			var current unix.Stat_t
			if unix.Fstatat(int(entry.parent.Fd()), entry.name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || current.Dev != pinned.Dev || current.Ino != pinned.Ino || current.Mode != pinned.Mode || current.Uid != pinned.Uid {
				return errors.New("workload directory replaced")
			}
		}
		if _, err := entry.file.Seek(0, io.SeekStart); err != nil {
			return errors.New("workload directory census unavailable")
		}
		// Read only the finite original member count plus one; an unowned
		// directory cannot force an unbounded census allocation.
		names, err := entry.file.Readdirnames(len(entry.members) + 1)
		if err != nil && err != io.EOF {
			return errors.New("workload directory census failed")
		}
		sort.Strings(names)
		sort.Strings(entry.members)
		if len(names) != len(entry.members) {
			return errors.New("workload closure contains unowned members")
		}
		for index, name := range names {
			if name != entry.members[index] {
				return errors.New("workload closure contains changed members")
			}
		}
	}
	return nil
}

func (owner *WorkloadInput) measureExisting(ctx context.Context, member WorkloadInputMember) error {
	for index, expected := range owner.snapshot.Members {
		if member == expected {
			return owner.measureMember(ctx, index, 0600)
		}
	}
	return errors.New("workload duplicate member outside owned closure")
}

// Decode only after every complete member hash passed. Each bounded metadata
// reread is independently hashed again before interpretation; no config Env or
// other private defaults escape the owner or comparison record.
func (owner *WorkloadInput) inspectGraph(ctx context.Context) error {
	manifestBytes, err := owner.readOriginalMetadata(ctx, "blobs/sha256/"+digest.Digest(owner.snapshot.ManifestDigest).Encoded(), 1<<20)
	if err != nil {
		return err
	}
	var manifest ocispec.Manifest
	if jsoncontract.Decode(manifestBytes, &manifest) != nil || manifest.Config.Digest.Validate() != nil || manifest.Config.Digest.Algorithm() != digest.SHA256 {
		return errors.New("workload local manifest metadata invalid")
	}
	configBytes, err := owner.readOriginalMetadata(ctx, "blobs/sha256/"+manifest.Config.Digest.Encoded(), 4<<20)
	if err != nil {
		return err
	}
	metadata, err := resolver.ValidateWorkloadImageMetadata(manifestBytes, configBytes, owner.snapshot.ManifestDigest, owner.snapshot.Architecture)
	if err != nil {
		return err
	}
	indexBytes, err := owner.readOriginalMetadata(ctx, "index.json", 4<<10)
	if err != nil {
		return err
	}
	expectedIndex, err := workloadIndexBytes(metadata, owner.snapshot.Architecture)
	if err != nil || !bytes.Equal(indexBytes, expectedIndex) {
		return errors.New("workload local index differs from exact manifest/platform")
	}
	layoutBytes, err := owner.readOriginalMetadata(ctx, "oci-layout", 128)
	if err != nil || !bytes.Equal(layoutBytes, []byte(`{"imageLayoutVersion":"1.0.0"}`)) {
		return errors.New("workload local layout version changed")
	}
	expected := map[string]WorkloadInputMember{}
	for _, source := range []struct {
		name    string
		payload []byte
	}{{"oci-layout", layoutBytes}, {"index.json", indexBytes}, {"blobs/sha256/" + metadata.ManifestDescriptor.Digest.Encoded(), manifestBytes}, {"blobs/sha256/" + metadata.ConfigDescriptor.Digest.Encoded(), configBytes}} {
		expected[source.name] = WorkloadInputMember{Name: source.name, Digest: digest.FromBytes(source.payload).String(), Size: int64(len(source.payload))}
	}
	for _, layer := range metadata.Layers {
		name := "blobs/sha256/" + layer.Digest.Encoded()
		expected[name] = WorkloadInputMember{Name: name, Digest: layer.Digest.String(), Size: layer.Size}
	}
	if len(expected) != len(owner.snapshot.Members) {
		return errors.New("workload local graph omits or adds manifest blobs")
	}
	for _, member := range owner.snapshot.Members {
		if expected[member.Name] != member {
			return errors.New("workload local graph descriptor changed")
		}
	}
	return nil
}
func (owner *WorkloadInput) readOriginalMetadata(ctx context.Context, name string, limit int64) ([]byte, error) {
	for index, member := range owner.snapshot.Members {
		if member.Name != name {
			continue
		}
		if member.Size <= 0 || member.Size > limit || owner.files[index] == nil {
			return nil, errors.New("workload metadata input bounds invalid")
		}
		file := owner.files[index]
		before, err := file.Stat()
		if err != nil || !workloadMemberOwner(int(file.Fd()), 0444) {
			return nil, errors.New("workload metadata owner unavailable")
		}
		payload, err := io.ReadAll(io.LimitReader(workloadContextReader{ctx: ctx, reader: io.NewSectionReader(file, 0, member.Size+1)}, member.Size+1))
		after, statErr := file.Stat()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil || statErr != nil || int64(len(payload)) != member.Size || digest.FromBytes(payload).String() != member.Digest || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !workloadMemberOwner(int(file.Fd()), 0444) {
			return nil, errors.New("workload original metadata bytes changed")
		}
		return payload, nil
	}
	return nil, errors.New("workload metadata outside original closure")
}

func workloadIndexBytes(metadata resolver.WorkloadImageMetadata, architecture string) ([]byte, error) {
	return json.Marshal(ocispec.Index{Versioned: specs.Versioned{SchemaVersion: 2}, MediaType: ocispec.MediaTypeImageIndex, Manifests: []ocispec.Descriptor{{MediaType: metadata.ManifestDescriptor.MediaType, Digest: metadata.ManifestDescriptor.Digest, Size: metadata.ManifestDescriptor.Size, Platform: &ocispec.Platform{OS: "linux", Architecture: architecture}}}})
}
