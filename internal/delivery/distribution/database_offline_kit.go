package distribution

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OfflineDatabaseUpdateKit is a closed, flat operator transfer directory. The
// index is locator DATA, not an endorsement. Signed OCI subjects and original
// signature manifests are authenticated before either full layer is opened.
// No extraction, arbitrary member paths, URLs, auth or policy fields exist.
type OfflineDatabaseUpdateKit struct {
	SchemaVersion int                         `json:"schema_version"`
	Repository    string                      `json:"repository"`
	Digest        string                      `json:"digest"`
	Subjects      []StoredSubject             `json:"subjects"`
	Metadata      []OfflineDatabaseUpdateBlob `json:"metadata"`
	Main          DatabaseDescriptor          `json:"main"`
	Java          DatabaseDescriptor          `json:"java"`
}

type OfflineDatabaseUpdateBlob struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

const MaximumOfflineDatabaseUpdateKitBytes int64 = 8 << 30

// CaptureOfflineUpdateKit accepts only the operator's existing private directory
// and complete canonical signed graph. It never creates a temp directory or
// network client before signature verification. Fixed file names make traversal,
// duplicate path aliases, links, nested extraction and expansion unnecessary.
func (authority *DatabaseAuthority) CaptureOfflineUpdateKit(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, directory string, now time.Time) (*VerifiedOfflineDatabaseUpdate, error) {
	if ctx == nil || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || !databaseUpdateUTC(now) {
		return nil, errors.New("offline database update kit root invalid")
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return nil, err
	}
	root, before, err := openSupplierTransferDirectory(directory, 1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	entries, err := root.ReadDir(28)
	if err != io.EOF && err != nil || len(entries) < 4 || len(entries) > 27 {
		return nil, errors.New("offline database update kit member count invalid")
	}
	indexPath := filepath.Join(directory, "index.json")
	index, err := readOfflineDatabaseKitMember(ctx, indexPath, 64<<10, "")
	if err != nil {
		return nil, err
	}
	var kit OfflineDatabaseUpdateKit
	if decodeClosed(index, &kit) != nil || kit.SchemaVersion != 1 || len(kit.Subjects) != 3 || len(kit.Metadata) == 0 || len(kit.Metadata) > 24 || kit.Main.validate("trivy-database") != nil || kit.Java.validate("trivy-java-database") != nil {
		return nil, errors.New("offline database update kit index invalid")
	}
	blobs := make(map[string][]byte, len(kit.Metadata))
	names := map[string]bool{"index.json": true, "main.tar.gz": true, "java.tar.gz": true}
	var metadataBytes int64
	for _, member := range kit.Metadata {
		if !digestPattern.MatchString(member.Digest) || member.Size <= 0 || member.Size > 1<<20 || blobs[member.Digest] != nil || member.Size > MaximumOfflineDatabaseUpdateMetadataBytes-metadataBytes {
			return nil, errors.New("offline database update kit metadata claim invalid")
		}
		name := strings.TrimPrefix(member.Digest, "sha256:")
		names[name] = true
		value, err := readOfflineDatabaseKitMember(ctx, filepath.Join(directory, name), member.Size, member.Digest)
		if err != nil {
			return nil, err
		}
		if int64(len(value)) != member.Size {
			return nil, errors.New("offline database update kit metadata size differs")
		}
		blobs[member.Digest] = value
		metadataBytes += member.Size
	}
	if len(entries) != len(names) {
		return nil, errors.New("offline database update kit contains extra or missing member")
	}
	for _, entry := range entries {
		if !names[entry.Name()] || !entry.Type().IsRegular() {
			return nil, errors.New("offline database update kit member kind or name differs")
		}
	}
	total := int64(len(index)) + metadataBytes + kit.Main.Size + kit.Java.Size
	if total > MaximumOfflineDatabaseUpdateKitBytes {
		return nil, errors.New("offline database update kit exceeds aggregate byte limit")
	}
	// Capture performs exact original signature-first graph authentication. Index
	// descriptors are compared afterward and can never substitute signed claims.
	captured, err := authority.CaptureOfflineUpdate(ctx, proof, OfflineDatabaseUpdateInput{Repository: kit.Repository, Digest: kit.Digest, Subjects: kit.Subjects, Blobs: blobs, MainPayloadPath: filepath.Join(directory, "main.tar.gz"), JavaPayloadPath: filepath.Join(directory, "java.tar.gz")}, now)
	if err != nil {
		return nil, err
	}
	_, _, main, java, valid := captured.Snapshot()
	current, statErr := os.Lstat(directory)
	opened, openErr := root.Stat()
	if !valid || main.Layer != kit.Main || java.Layer != kit.Java || statErr != nil || openErr != nil || !current.IsDir() || !os.SameFile(before, current) || !os.SameFile(before, opened) || current.Mode() != before.Mode() || !current.ModTime().Equal(before.ModTime()) {
		captured.Close()
		return nil, errors.New("offline database update kit changed or index differs from signed layer")
	}
	return captured, nil
}

func readOfflineDatabaseKitMember(ctx context.Context, path string, maximum int64, expected string) ([]byte, error) {
	if ctx == nil || maximum <= 0 || maximum > 1<<20 {
		return nil, errors.New("offline database update metadata bound invalid")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maximum {
		return nil, errors.New("offline database update metadata regular member unavailable")
	}
	file, identity, err := openDatabaseInspectionFile(path, before.Size())
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	value, err := io.ReadAll(io.LimitReader(databaseInspectionReader{ctx: ctx, input: file}, maximum+1))
	if err != nil {
		return nil, databaseInspectionError(ctx, "offline database update metadata read failed")
	}
	if int64(len(value)) != before.Size() || !sameDatabaseInspectionFile(file, path, identity) || expected != "" && contentDigest(value) != expected {
		return nil, errors.New("offline database update metadata member changed")
	}
	return value, nil
}
