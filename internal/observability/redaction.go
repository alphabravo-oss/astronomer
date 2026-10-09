package observability

import (
	"io"
	"log/slog"
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

// These are diagnostic fields, not the public authentication wire contract.
// Complex arbitrary objects are omitted rather than serialized without a field
// policy. Callers should log safe named scalar fields and correlation IDs.
func sensitiveDiagnosticKey(key string) bool {
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

// ScrubDiagnosticText removes recognized credential representations in errors
// and messages. It cannot identify arbitrary unlabelled secret strings. Bodies,
// manifests and credentials must never be interpolated into diagnostic text.
func ScrubDiagnosticText(value string) string {
	for _, pattern := range diagnosticPatterns {
		value = pattern.ReplaceAllString(value, redacted)
	}
	return value
}

// NewJSONHandler is the common server/worker/agent diagnostic boundary.
func NewJSONHandler(writer io.Writer, options *slog.HandlerOptions) *slog.JSONHandler {
	var copyOptions slog.HandlerOptions
	if options != nil {
		copyOptions = *options
	}
	previous := copyOptions.ReplaceAttr
	copyOptions.ReplaceAttr = func(groups []string, attr slog.Attr) slog.Attr {
		if previous != nil {
			attr = previous(groups, attr)
		}
		for _, group := range groups {
			if sensitiveDiagnosticKey(group) {
				attr.Value = slog.StringValue(redacted)
				return attr
			}
		}
		if sensitiveDiagnosticKey(attr.Key) {
			attr.Value = slog.StringValue(redacted)
			return attr
		}
		switch attr.Value.Kind() {
		case slog.KindString:
			attr.Value = slog.StringValue(ScrubDiagnosticText(attr.Value.String()))
		case slog.KindAny:
			if err, ok := attr.Value.Any().(error); ok {
				attr.Value = diagnosticError(err)
			} else {
				attr.Value = slog.StringValue("[OMITTED structured diagnostic]")
			}
		}
		return attr
	}
	return slog.NewJSONHandler(writer, &copyOptions)
}
