package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

// UnmarshalJSON retains the exact request DTO boundary and validates nested
// fields before Go zero values can turn null/missing/wrongly typed matchers into
// valid durable records. The published matcher label/value keys are required;
// an empty value remains meaningful and omitted is_regex still means false.
func (request *InhibitionRequest) UnmarshalJSON(raw []byte) error {
	type plain InhibitionRequest
	var decoded plain
	r := &http.Request{Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw))}
	if err := jsoncontract.DecodeRequest(r, &decoded, jsoncontract.ExactProduct); err != nil {
		return err
	}
	var fields struct {
		Source json.RawMessage `json:"source_matchers"`
		Target json.RawMessage `json:"target_matchers"`
		Equal  json.RawMessage `json:"equal_labels"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	// This is a request-only v1 adapter, matching marshalEqualLabels. Historical
	// durable JSON null is not normalized by the shared stored-record reader.
	if len(fields.Equal) == 0 || bytes.Equal(bytes.TrimSpace(fields.Equal), []byte("null")) {
		fields.Equal = []byte("[]")
	}
	if _, err := jsoncontract.ReadInhibitionV1(fields.Source, fields.Target, fields.Equal); err != nil {
		return err
	}
	*request = InhibitionRequest(decoded)
	return nil
}
