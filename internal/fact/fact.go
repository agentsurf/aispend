// Package fact defines the unit of collected data and the envelope it travels
// in. Both are deliberately dumb: no database, no network, no vendor knowledge.
package fact

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Fact is one row of usage: what a vendor charged, for one day, one principal,
// one model. It is an aggregate — never a prompt, never a request, never
// anything that identifies what a person asked.
//
// Money is micros (USD × 1e6) as an int64 throughout. Floating-point money in a
// tool whose entire value is a defensible number is an unforced error, and the
// JSON tags carry the raw integer for the same reason.
type Fact struct {
	Vendor       string `json:"vendor"`
	Day          string `json:"day"` // YYYY-MM-DD, UTC. Never local time.
	WorkspaceRef string `json:"workspace_ref"`
	PrincipalRef string `json:"principal_ref"`
	ModelRef     string `json:"model_ref"`

	InputUnits  int64 `json:"input_units"`
	OutputUnits int64 `json:"output_units"`
	// CachedUnits is tokens *read* from cache, priced at a steep discount.
	// Folding them into InputUnits produces a number wrong by a margin that
	// grows as the customer optimises — exactly when they are checking your
	// work.
	CachedUnits int64 `json:"cached_units"`
	// CacheWriteUnits is tokens written *to* cache, priced at a premium over
	// base input. It is separate from CachedUnits because the two move the
	// number in opposite directions, so combining them does not even average
	// out — it produces a figure that is wrong either way.
	CacheWriteUnits int64  `json:"cache_write_units"`
	OtherUnits      int64  `json:"other_units"` // seats, characters, images
	UnitKind        string `json:"unit_kind"`   // token | character | seat_day | request

	AmountMicros int64  `json:"amount_micros"`
	AmountBasis  Basis  `json:"amount_basis"`
	PriceVersion string `json:"price_version,omitempty"`

	Revision    int       `json:"revision"`
	CollectedAt time.Time `json:"collected_at"`
}

// Basis records where an amount came from. It is load-bearing: the difference
// between "the vendor told us this cost $8,204" and "we computed $3,266 from our
// price book" is what makes a finance person trust the tool, and it is surfaced
// in the report's footer rather than hidden in the schema.
type Basis string

const (
	// BasisVendorReported — the vendor gave us this money figure directly.
	BasisVendorReported Basis = "vendor_reported"
	// BasisAllocated — the vendor reported cost more coarsely than model level
	// and we split it down using the price book.
	BasisAllocated Basis = "allocated"
	// BasisComputed — the vendor reported no money at all; the price book
	// produced this from unit counts.
	BasisComputed Basis = "computed"
	// BasisUnknown — we could not price it. The fact is still emitted, with a
	// zero amount and a warning, because dropping the row would silently
	// understate the total and "unknown" is not "zero".
	BasisUnknown Basis = "unknown"
)

// sep separates fields inside the hash pre-image. It is the ASCII unit
// separator, which cannot appear in any of the identifiers a vendor issues, so
// ("a", "bc") and ("ab", "c") can never collide.
const sep = "\x1f"

// ID is a deterministic identity for the fact's dimensions, stable across
// machines and across runs.
//
// The database's primary key is local — it identifies a row in one file. Once
// facts leave the machine (the sidecar posture in design §9.2), a server
// receiving them from many agents needs to dedupe and absorb restatements
// idempotently, and agents retry, resend and overlap windows constantly. The
// column costs one line today and cannot be added cleanly to a fleet already in
// the field.
//
// Revision is deliberately excluded: every revision of a day's usage is the same
// fact restated, and the server needs to recognise it as such.
func (f Fact) ID() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		f.Vendor, f.Day, f.WorkspaceRef, f.PrincipalRef, f.ModelRef,
	}, sep)))
	return hex.EncodeToString(sum[:])
}

// TotalUnits is every unit the fact counts, for displays that want one number.
func (f Fact) TotalUnits() int64 {
	return f.InputUnits + f.OutputUnits + f.CachedUnits + f.CacheWriteUnits + f.OtherUnits
}

// Envelope wraps a batch of facts for anything that leaves this process.
//
// v1 never transmits one — the only sink is SQLite. It exists now because the
// moment facts do leave, agent and server version independently and will drift;
// a server that rejects unknown majors and tolerates unknown fields is free to
// build today and impossible to retrofit across agents that cannot all be
// upgraded at once.
type Envelope struct {
	Schema    int       `json:"schema"`
	Agent     string    `json:"agent"`
	InstallID string    `json:"install_id"`
	SentAt    time.Time `json:"sent_at"`
	Facts     []Fact    `json:"facts"`
}

// EnvelopeSchema is the current major version of the envelope format.
const EnvelopeSchema = 1
