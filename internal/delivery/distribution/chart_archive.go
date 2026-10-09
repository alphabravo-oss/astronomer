package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
)

// ValidateChartArchive closes nested Helm dependency archives before they reach
// Helm's loader. It rejects links, special members, duplicate paths, traversal,
// concatenated streams, incomplete compression, and oversized recursion.
func ValidateChartArchive(payload []byte) error {
	budget := chartBudget{}
	return validateChartArchive(payload, 0, &budget)
}

type chartBudget struct {
	members  int
	expanded int64
}

func validateChartArchive(payload []byte, depth int, budget *chartBudget) error {
	if depth > 8 || int64(len(payload)) > MaximumArchiveBytes {
		return errors.New("recursive chart archive exceeds bounds")
	}
	input := bytes.NewReader(payload)
	compressed, err := gzip.NewReader(input)
	if err != nil {
		return errors.New("chart compression is invalid")
	}
	defer compressed.Close()
	compressed.Multistream(false)
	expanded, err := io.ReadAll(io.LimitReader(compressed, MaximumExpandedBytes+1))
	if err != nil || int64(len(expanded)) > MaximumExpandedBytes || input.Len() != 0 {
		return errors.New("chart compression exceeds bounds or has trailing data")
	}
	budget.expanded += int64(len(expanded))
	if budget.expanded > MaximumExpandedBytes {
		return errors.New("recursive chart expansion exceeds bounds")
	}
	raw := bytes.NewReader(expanded)
	archive := tar.NewReader(raw)
	seen := map[string]bool{}
	chartMetadata := false
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("chart tar archive is invalid")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !memberPath(name) || seen[name] || header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir || header.Size < 0 || header.Size > MaximumArchiveBytes {
			return errors.New("chart tar member is unsafe")
		}
		seen[name] = true
		budget.members++
		if budget.members > MaximumMembers {
			return errors.New("recursive chart member limit exceeded")
		}
		if strings.HasSuffix(name, "/Chart.yaml") {
			chartMetadata = true
		}
		if header.Typeflag == tar.TypeReg && strings.Contains(name, "/charts/") && strings.HasSuffix(name, ".tgz") {
			nested, err := io.ReadAll(io.LimitReader(archive, header.Size+1))
			if err != nil || int64(len(nested)) != header.Size {
				return errors.New("chart dependency size mismatch")
			}
			if err := validateChartArchive(nested, depth+1, budget); err != nil {
				return err
			}
		}
	}
	if trailing, _ := io.ReadAll(raw); len(bytes.Trim(trailing, "\x00")) > 0 || !chartMetadata {
		return errors.New("chart archive has trailing payload or lacks metadata")
	}
	return nil
}
