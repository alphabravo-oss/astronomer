package email

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime/quotedprintable"
	"strings"
	"time"
)

// composeMessage assembles a multipart/alternative RFC 5322 message
// from the rendered text + html bodies. We hand-roll the bytes rather
// than using net/mail.Message because the latter only serialises a
// single body — multipart with the right Content-Type boundary needs
// to be assembled directly.
//
// Encoding choices:
//   * Subject is rendered ASCII-only (asciiSafeSubject) so we don't
//     need to RFC 2047-encode.
//   * Body parts use quoted-printable so a "long line" or stray UTF-8
//     in a username doesn't trip SMTP servers that enforce the 1000-
//     octet hard line limit (RFC 5321 §4.5.3.1.6).
//   * The boundary is 32 random bytes hex-encoded; collision with body
//     content is statistically impossible at that length.
func composeMessage(subject, from, to, cc, text, html string) ([]byte, error) {
	boundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	if cc != "" {
		fmt.Fprintf(&buf, "Cc: %s\r\n", cc)
	}
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary)
	fmt.Fprintf(&buf, "\r\n")

	// Text part
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	if err := writeQuotedPrintable(&buf, text); err != nil {
		return nil, err
	}
	fmt.Fprintf(&buf, "\r\n")

	// HTML part
	fmt.Fprintf(&buf, "--%s\r\n", boundary)
	fmt.Fprintf(&buf, "Content-Type: text/html; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	if err := writeQuotedPrintable(&buf, html); err != nil {
		return nil, err
	}
	fmt.Fprintf(&buf, "\r\n")

	// Closing boundary
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)

	return buf.Bytes(), nil
}

func writeQuotedPrintable(buf *bytes.Buffer, body string) error {
	w := quotedprintable.NewWriter(buf)
	if _, err := w.Write([]byte(strings.ReplaceAll(body, "\r\n", "\n"))); err != nil {
		return err
	}
	return w.Close()
}

func randomBoundary() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "astronomer-" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}
