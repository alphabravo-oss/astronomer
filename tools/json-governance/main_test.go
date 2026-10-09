package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverRequestImportAliasesAndBindings(t *testing.T) {
	source := `package example
import (j "encoding/json"; h "net/http")
func request(incoming *h.Request) { j.NewDecoder(incoming.Body) }
func response(incoming *h.Response) { j.NewDecoder(incoming.Body) }
func unrelated() { value := 1; _ = value }
`
	path := filepath.Join(t.TempDir(), "example.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	requests, calls, err := discoverSites(path, "example.go")
	if err != nil || len(requests) != 1 || requests[0].Function != "request" || len(calls) != 2 {
		t.Fatalf("request binding discovery: requests=%+v calls=%+v err=%v", requests, calls, err)
	}
}

func TestDiscoverPersistenceJSONColumns(t *testing.T) {
	source := "package sqlc\nimport j \"encoding/json\"\ntype Row struct {\nPayload j.RawMessage `json:\"payload\"`\nName string `json:\"name\"`\n}"
	path := filepath.Join(t.TempDir(), "models.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	columns, err := discoverColumns(path)
	if err != nil || len(columns) != 1 || columns[0].Model != "Row" || columns[0].Column != "payload" {
		t.Fatalf("persistence discovery: columns=%+v err=%v", columns, err)
	}
}

func TestDiscoverByteAndEncryptedPersistenceSlots(t *testing.T) {
	source := "package sqlc\ntype Row struct {\nPayload []byte `json:\"payload\"`\nCipher string `json:\"document_encrypted\"`\nName string `json:\"name\"`\n}"
	path := filepath.Join(t.TempDir(), "models.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	columns, err := discoverColumns(path)
	if err != nil || len(columns) != 2 || columns[0].Storage != "byte_slot" || columns[1].Storage != "encrypted_slot" {
		t.Fatalf("unclassified JSON-capable storage: %+v %v", columns, err)
	}
}

func TestDiscoverGenericRequestHelperBinding(t *testing.T) {
	source := `package example
import h "net/http"
func request(incoming *h.Request) { decodeAndValidate[Payload](writer,incoming) }
func response(incoming *h.Response) { decodeAndValidate[Payload](writer,incoming) }
`
	path := filepath.Join(t.TempDir(), "example.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	requests, _, err := discoverSites(path, "example.go")
	if err != nil || len(requests) != 1 || requests[0].Function != "request" {
		t.Fatalf("generic HTTP binding: %+v %v", requests, err)
	}
}

func TestDiscoverDoesNotBindShadowedAliasesOrNestedRequests(t *testing.T) {
	source := `package example
import (j "encoding/json"; h "net/http")
func request(incoming *h.Request) {
 j.NewDecoder(incoming.Body)
 func(incoming *h.Response) { j.NewDecoder(incoming.Body); decodeV1JSONBody(incoming, &value) }(response)
}
func local(incoming *h.Request) { j := unrelated; j.NewDecoder(incoming.Body) }
`
	path := filepath.Join(t.TempDir(), "example.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	requests, calls, err := discoverSites(path, "example.go")
	if err != nil || len(requests) != 1 || len(calls) != 2 {
		t.Fatalf("shadowed binding accepted: requests=%+v calls=%+v err=%v", requests, calls, err)
	}
}

func TestDiscoverNestedBodyExpressionsDoNotPanicOrBindUnrelatedRequest(t *testing.T) {
	source := `package example
import (j "encoding/json"; h "net/http")
func nested(incoming *h.Request,response *h.Response) {
 j.NewDecoder(response.HTTPResponse.Body)
 j.NewDecoder(response.Read().Body)
 j.NewDecoder(incoming.Body)
 func(incoming *h.Response) { j.NewDecoder(incoming.Body) }(response)
}`
	path := filepath.Join(t.TempDir(), "example.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	requests, calls, err := discoverSites(path, "example.go")
	if err != nil || len(requests) != 1 || len(calls) != 4 {
		t.Fatalf("nested body binding: requests=%+v calls=%+v err=%v", requests, calls, err)
	}
}
