package drcrypto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// CanonicalJSON matches Python json.dumps(sort_keys=True,
// separators=(",", ":"), ensure_ascii=True): sorted keys, no whitespace,
// non-ASCII escaped as \uXXXX (surrogate pairs above the BMP).
func CanonicalJSON(value any) ([]byte, error) {
	// Round-trip through a generic value so struct fields are key-sorted.
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(generic); err != nil {
		return nil, err
	}
	out := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	var ascii bytes.Buffer
	for len(out) > 0 {
		r, size := utf8.DecodeRune(out)
		switch {
		case r < 0x80:
			ascii.WriteByte(out[0])
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&ascii, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&ascii, `\u%04x`, r)
		}
		out = out[size:]
	}
	return ascii.Bytes(), nil
}

// decodeClosed decodes JSON rejecting unknown fields and trailing data.
func decodeClosed(raw []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("trailing JSON data")
	}
	return nil
}

func requireRegularFile(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return failf("%s must be a regular file, not a link", label)
	}
	return nil
}

// writeAtomically writes a 0600 temporary file next to destination and
// renames it into place only when fill succeeds. It refuses to overwrite.
func writeAtomically(destination string, fill func(io.Writer) error) error {
	if _, err := os.Lstat(destination); err == nil {
		return failf("refusing to overwrite output %s", destination)
	}
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(destination)+".")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(name) }
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if err := fill(tmp); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, destination); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// validateObjectKey accepts a relative, normalized object key without
// whitespace, backslashes or dot segments.
func validateObjectKey(value, field string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || strings.ContainsFunc(value, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' }) {
		return failf("%s is not a safe object key", field)
	}
	for part := range strings.SplitSeq(value, "/") {
		if part == "" || part == "." || part == ".." {
			return failf("%s is not a safe object key", field)
		}
	}
	return nil
}
