// Package toolchain exposes immutable bytes of the canonical authority compiled
// with the consumer. It is not a second version selection or runtime discovery.
package toolchain

import _ "embed"

//go:embed versions.json
var versions []byte

//go:embed trust/trusted_root.json
var trustedRoot []byte

func AuthorityBytes() []byte   { return append([]byte(nil), versions...) }
func TrustedRootBytes() []byte { return append([]byte(nil), trustedRoot...) }
