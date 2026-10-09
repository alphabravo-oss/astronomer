package jsoncontract

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// MaxRequestBytes retains the existing REST middleware's 32 MiB ceiling.
// Narrower route/middleware limits remain effective around this decoder.
const MaxRequestBytes int64 = 32 << 20

type RequestPolicy uint8

const (
	ExactProduct RequestPolicy = iota
	// AdditiveV1 preserves published v1 clients whose extra properties were
	// ignored. Nested raw Kubernetes and provider objects remain uninterpreted.
	AdditiveV1
)

// DecodeRequest is the bounded, single-document reader. It retains io.EOF for
// documented optional bodies, rejects top-level null instead of fabricating a
// zero DTO, and applies unknown-field policy only at typed struct boundaries.
func DecodeRequest(r *http.Request, out any, policy RequestPolicy) error {
	if policy != ExactProduct && policy != AdditiveV1 {
		return &Error{"policy", "unsupported"}
	}
	if r.Body == nil {
		return io.EOF
	}
	if r.ContentLength > MaxRequestBytes {
		return &http.MaxBytesError{Limit: MaxRequestBytes}
	}
	r.Body = http.MaxBytesReader(nil, r.Body, MaxRequestBytes)
	reader := bufio.NewReader(r.Body)
	for {
		first, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if first == ' ' || first == '\n' || first == '\r' || first == '\t' {
			continue
		}
		if first == 'n' {
			return &Error{"body", "invalid_shape"}
		}
		if err := reader.UnreadByte(); err != nil {
			return err
		}
		break
	}
	decoder := json.NewDecoder(reader)
	if policy == ExactProduct {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return &Error{"body", "trailing_document"}
	}
	return err
}
