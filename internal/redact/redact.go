// Package redact is the last line of defence against a credential reaching a
// terminal.
//
// Everything upstream already avoids printing secrets: the cred type redacts in
// every format verb, error messages never carry a response body, and no output
// format has a field for a key. This exists for the paths nobody thought about
// — a panic printing a struct, a vendor echoing a key back inside an error, a
// future feature written in a hurry.
package redact

import (
	"io"
	"regexp"
	"sync"
)

// patterns match credential-shaped strings.
//
// Deliberately shaped rather than exhaustive: matching "any long random string"
// would redact fact ids, cursors and hashes, and output full of ██ is output
// nobody reads. These cover the prefixes the three v1 vendors actually issue,
// plus the generic bearer-token header.
var patterns = []*regexp.Regexp{
	// OpenAI: sk-…, sk-proj-…, sk-svcacct-…
	regexp.MustCompile(`sk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_-]{16,}`),
	// Anthropic: sk-ant-admin01-…, sk-ant-api03-…
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{16,}`),
	// OpenRouter: sk-or-v1-…
	regexp.MustCompile(`sk-or-[A-Za-z0-9_-]{16,}`),
	// Anything presented as a bearer token or an api key header.
	regexp.MustCompile(`(?i)(authorization:\s*bearer\s+|x-api-key:\s*)\S+`),
}

// Mask is what replaces a match. It is visible on purpose: silently dropping
// the text would leave a reader wondering whether the tool mangled something.
const Mask = "[redacted]"

// Writer wraps a writer and scrubs credential-shaped strings from everything
// written through it.
type Writer struct {
	mu sync.Mutex
	w  io.Writer
}

// New wraps w.
func New(w io.Writer) *Writer { return &Writer{w: w} }

func (r *Writer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cleaned := Bytes(p)
	if _, err := r.w.Write(cleaned); err != nil {
		return 0, err
	}
	// Report the caller's length, not the cleaned one. A short write would look
	// like an I/O error to the caller and could send it into a retry loop.
	return len(p), nil
}

// Bytes scrubs a byte slice.
func Bytes(p []byte) []byte {
	for _, re := range patterns {
		p = re.ReplaceAllFunc(p, func(match []byte) []byte {
			// Keep the header name so the line still reads sensibly.
			if idx := headerPrefix(match); idx > 0 {
				return append(append([]byte{}, match[:idx]...), Mask...)
			}
			return []byte(Mask)
		})
	}
	return p
}

// String scrubs a string.
func String(s string) string { return string(Bytes([]byte(s))) }

// headerPrefix finds where a header's value starts, so "Authorization: Bearer "
// survives and only the token is replaced.
func headerPrefix(match []byte) int {
	for i := len(match) - 1; i > 0; i-- {
		if match[i] == ' ' || match[i] == ':' {
			return i + 1
		}
	}
	return 0
}
