package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Redacted replaces the value of every sensitive field.
const Redacted = "[REDACTED]"

// omittedStructured replaces arbitrary structured slog values: callers log
// safe named scalar fields, never whole objects without a field policy.
const omittedStructured = "[OMITTED structured diagnostic]"

// SensitiveKey reports whether a log/span field name must be redacted. These
// are diagnostic fields, not the authentication wire contract.
func SensitiveKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
	switch key {
	case "authorization", "cookie", "setcookie", "password", "passwd", "credential", "credentials", "secret", "secretkey", "privatekey", "token", "accesstoken", "refreshtoken", "apikey", "clientsecret", "encryptionkey", "kubeconfig", "manifest", "requestbody", "responsebody", "body", "payload", "shellinput", "shelloutput", "stdin", "stdout", "stderr":
		return true
	}
	return strings.HasPrefix(key, "httprequestheader") || strings.HasPrefix(key, "httpresponseheader") || key == "dbstatement" || key == "dbquerytext" || key == "dbconnectionstring" || key == "processcommandargs" || key == "exceptionmessage" || key == "errormessage"
}

var diagnosticPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`(?im)\b(?:authorization|set-cookie|cookie)\s*:\s*[^\r\n]+`),
	regexp.MustCompile(`(?i)\bBearer\s+[^\s,;"']+`),
	regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|https?|redis(?:s)?)://[^\s/]+@`),
	regexp.MustCompile(`(?i)\b(?:password|passwd|token|secret|access_token|refresh_token|api_key|client_secret)\s*[=:]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`astronomer:[^\s"']+`),
}

// Scrub removes recognised credential representations from free text. It
// cannot identify arbitrary unlabelled secrets: bodies, manifests and
// credentials must never be interpolated into diagnostic text.
func Scrub(value string) string {
	for _, pattern := range diagnosticPatterns {
		value = pattern.ReplaceAllString(value, Redacted)
	}
	return value
}

// marshalError is zerolog.ErrorMarshalFunc: error categories whose messages
// echo user-supplied literals keep only their safe classification.
func marshalError(err error) interface{} {
	if err == nil {
		return nil
	}
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) {
		// Message, Detail, Where and SQL can echo user-supplied literals.
		return safeError{"type": "postgres", "sqlstate": postgres.Code}
	}
	var kubernetes *apierrors.StatusError
	if errors.As(err, &kubernetes) {
		// Status.Message/Details can embed rejected Secret manifests.
		return safeError{"type": "kubernetes", "http_status": int(kubernetes.ErrStatus.Code), "reason": kubernetesReason(string(kubernetes.ErrStatus.Reason))}
	}
	return Scrub(err.Error())
}

type safeError map[string]any

func (fields safeError) MarshalZerologObject(event *zerolog.Event) {
	for _, key := range []string{"type", "sqlstate", "http_status", "reason"} {
		if value, ok := fields[key]; ok {
			event.Interface(key, value)
		}
	}
}

func kubernetesReason(reason string) string {
	switch reason {
	case "Unauthorized", "Forbidden", "NotFound", "AlreadyExists", "Conflict", "Invalid", "Expired", "Gone", "ServerTimeout", "Timeout", "TooManyRequests", "BadRequest", "MethodNotAllowed", "NotAcceptable", "UnsupportedMediaType", "InternalError", "ServiceUnavailable", "RequestEntityTooLarge":
		return reason
	default:
		return "Unknown"
	}
}

// redactingWriter is the redaction boundary: it re-encodes every JSON line,
// preserving key order, with sensitive subtrees replaced and every string
// scrubbed. Writes are serialised so any sink is safe for concurrent use.
type redactingWriter struct {
	mu   sync.Mutex
	next io.Writer
}

func (w *redactingWriter) Write(line []byte) (int, error) {
	clean, err := RedactJSON(line)
	if err != nil {
		// zerolog only emits JSON; a malformed line is replaced rather than
		// passed through unredacted.
		clean, _ = json.Marshal(map[string]string{"level": "error", "message": "log line dropped: " + Scrub(err.Error())})
		clean = append(clean, '\n')
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.next.Write(clean); err != nil {
		return 0, err
	}
	return len(line), nil
}

// RedactJSON applies the field and text redaction policy to one JSON object
// line and returns it re-encoded (newline-terminated).
func RedactJSON(line []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	var out bytes.Buffer
	out.Grow(len(line))
	if err := redactValue(decoder, &out); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

func redactValue(decoder *json.Decoder, out *bytes.Buffer) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			return redactObject(decoder, out)
		case '[':
			return redactArray(decoder, out)
		default:
			return errors.New("unexpected delimiter")
		}
	case string:
		writeString(out, Scrub(value))
	case json.Number:
		out.WriteString(value.String())
	case bool:
		if value {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case nil:
		out.WriteString("null")
	}
	return nil
}

func redactObject(decoder *json.Decoder, out *bytes.Buffer) error {
	out.WriteByte('{')
	for first := true; decoder.More(); first = false {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("object key is not a string")
		}
		if !first {
			out.WriteByte(',')
		}
		writeString(out, key)
		out.WriteByte(':')
		if SensitiveKey(key) {
			var skipped json.RawMessage
			if err := decoder.Decode(&skipped); err != nil {
				return err
			}
			writeString(out, Redacted)
			continue
		}
		if err := redactValue(decoder, out); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	out.WriteByte('}')
	return nil
}

func redactArray(decoder *json.Decoder, out *bytes.Buffer) error {
	out.WriteByte('[')
	for first := true; decoder.More(); first = false {
		if !first {
			out.WriteByte(',')
		}
		if err := redactValue(decoder, out); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	out.WriteByte(']')
	return nil
}

func writeString(out *bytes.Buffer, value string) {
	encoded, _ := json.Marshal(value)
	out.Write(encoded)
}
