package collect

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/egress"
)

// VendorError is a failure talking to one vendor, in the what/why/fix shape the
// design requires of every error surface.
//
// The three cases below are kept apart on purpose. Telling someone behind a
// corporate proxy that their key is bad sends them to regenerate a perfectly
// good admin key, waste an hour, and conclude the tool is broken.
type VendorError struct {
	Vendor string
	What   string // one line: what happened
	Why    string // what it means
	Fix    string // the command or action that resolves it
	Err    error
}

func (e *VendorError) Error() string { return e.What }
func (e *VendorError) Unwrap() error { return e.Err }

// Blocked reports whether this failure was the egress guard refusing a host.
func (e *VendorError) Blocked() bool {
	var b *egress.BlockedError
	return errors.As(e.Err, &b)
}

// classify turns a transport error into the right explanation. Reachability and
// rejection are different facts and must never be reported as each other.
//
// The unwrap below is load-bearing. http.Client wraps everything it returns in
// *url.Error, which itself satisfies net.Error — so a naive errors.As(err,
// &netErr) matches every failure, including ones that never touched the
// network, and reports them all as "check your firewall". That is the same
// misdiagnosis in the other direction: sending someone to debug their proxy
// over a malformed fixture file.
func classify(vendorID string, err error) *VendorError {
	if urlErr := (*url.Error)(nil); errors.As(err, &urlErr) {
		err = urlErr.Err
	}

	var blocked *egress.BlockedError
	if errors.As(err, &blocked) {
		return &VendorError{
			Vendor: vendorID,
			What:   fmt.Sprintf("egress to %s was blocked", blocked.Host),
			Why:    "That host is not in the vendor catalog compiled into this binary.",
			Fix:    "This is a bug in aispend, not in your setup — please report it.",
			Err:    err,
		}
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &VendorError{
			Vendor: vendorID,
			What:   fmt.Sprintf("could not resolve %s", dnsErr.Name),
			Why:    "The name did not resolve. This is a network or DNS problem, not a credential problem — your key has not been tested yet.",
			Fix:    "Check your connection or DNS, then try again.",
			Err:    err,
		}
	}

	// Only a genuine network-layer failure earns the connectivity explanation:
	// a socket operation that failed, or something that actually timed out.
	var opErr *net.OpError
	var netErr net.Error
	if errors.As(err, &opErr) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return &VendorError{
			Vendor: vendorID,
			What:   "could not open a connection",
			Why:    "The connection failed or timed out before the vendor answered. Your credential has not been tested — a proxy or firewall is the usual cause.",
			Fix:    "Check outbound HTTPS access, then: aispend scan --dry-run  (prints every host aispend needs)",
			Err:    err,
		}
	}

	// Anything else — a bad fixture, a TLS failure, a bug — is reported as
	// itself rather than blamed on the network or the credential.
	return &VendorError{
		Vendor: vendorID,
		What:   "the request failed",
		Why:    err.Error(),
		Fix:    "Run with --debug for the full detail.",
		Err:    err,
	}
}

// httpError explains a response the vendor did send. The vendor answered, so
// this is never a connectivity problem.
func httpError(vendorID string, status int, body string) *VendorError {
	v, _ := catalog.Get(vendorID)
	e := &VendorError{
		Vendor: vendorID,
		What:   fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Err:    fmt.Errorf("http %d", status),
	}

	switch status {
	case http.StatusUnauthorized:
		e.Why = "The key was rejected. It may be revoked, mistyped, or from a different organisation."
		e.Fix = fmt.Sprintf("Check the value of %s, or create a new %s at %s",
			firstEnv(v), v.Credential.Kind, v.Credential.Where)
	case http.StatusForbidden:
		e.Why = fmt.Sprintf("The key was accepted but is not authorised to read organisation usage. %s", v.Credential.Note)
		e.Fix = fmt.Sprintf("Create a %s at %s, then set %s", v.Credential.Kind, v.Credential.Where, firstEnv(v))
	case http.StatusNotFound:
		e.Why = "The endpoint does not exist for this account. Some usage APIs require an organisation rather than an individual account."
		e.Fix = "Check that your account is part of an organisation, not an individual plan."
	case http.StatusTooManyRequests:
		e.Why = "The vendor is rate limiting this key."
		e.Fix = "Wait a minute and try again."
	default:
		if status >= 500 {
			e.Why = "The vendor returned a server error. Nothing is wrong with your key."
			e.Fix = "Try again shortly."
		} else {
			e.Why = "The vendor rejected the request."
			e.Fix = "Run with --debug for the request detail."
		}
	}

	// The body may contain anything, including a credential the vendor echoed
	// back. It goes to the debug channel, never to the user's terminal.
	_ = body
	return e
}

func firstEnv(v catalog.Vendor) string {
	if len(v.Credential.Env) == 0 {
		return "the vendor's environment variable"
	}
	return v.Credential.Env[0]
}
