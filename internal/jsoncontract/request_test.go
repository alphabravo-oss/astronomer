package jsoncontract

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRequestPolicyAndOneDocument(t *testing.T) {
	for _, policy := range []RequestPolicy{ExactProduct, AdditiveV1} {
		for _, body := range []string{`null`, `{} {}`, `{} false`, `{"name":1}`, `{"name":"ok"} trailing`} {
			var out struct {
				Name string `json:"name"`
			}
			if err := DecodeRequest(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &out, policy); err == nil {
				t.Fatalf("policy %d accepted invalid document %s", policy, body)
			}
		}
		var out struct {
			Name string `json:"name"`
		}
		err := DecodeRequest(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok","future":true}`)), &out, policy)
		if (err == nil) != (policy == AdditiveV1) {
			t.Fatalf("unknown-property policy changed: policy=%d err=%v", policy, err)
		}
		if err := DecodeRequest(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(" \n\t")), &out, policy); !errors.Is(err, io.EOF) {
			t.Fatalf("optional empty body no longer reports EOF: %v", err)
		}
	}
}

func TestDecodeRequestPreservesOpaqueNestedProviderAndKubernetes(t *testing.T) {
	var out struct {
		Provider json.RawMessage `json:"provider"`
		Object   map[string]any  `json:"object"`
	}
	body := `{"provider":{"vendor_future":{"value":null}},"object":{"apiVersion":"future.test/v1","spec":{"unknown":{"foo":1}}}}`
	if err := DecodeRequest(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &out, ExactProduct); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Provider), "vendor_future") || out.Object["spec"] == nil {
		t.Fatal("external fields were lost")
	}
	malformed := `{"provider":{"vendor_future":}}`
	if err := DecodeRequest(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(malformed)), &out, AdditiveV1); err == nil {
		t.Fatal("malformed provider JSON cannot become a valid document")
	}
}

func TestDecodeRequestSizeBoundWithoutContentLength(t *testing.T) {
	var out struct {
		Name string `json:"name"`
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"`+strings.Repeat("a", int(MaxRequestBytes))+`"}`))
	r.ContentLength = -1
	var tooLarge *http.MaxBytesError
	if err := DecodeRequest(r, &out, AdditiveV1); !errors.As(err, &tooLarge) {
		t.Fatalf("chunked standalone body exceeded bound: %v", err)
	}
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"`+strings.Repeat("a", 128)+`"}`))
	r.Body = http.MaxBytesReader(httptest.NewRecorder(), r.Body, 64)
	if err := DecodeRequest(r, &out, AdditiveV1); !errors.As(err, &tooLarge) || tooLarge.Limit != 64 {
		t.Fatalf("canonical helper weakened smaller route limit: %v", err)
	}
}
