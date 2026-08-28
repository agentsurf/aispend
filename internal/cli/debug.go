package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/buildinfo"
	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/fact"
	"github.com/prabhuvmk/aispend/internal/sink"
	"github.com/prabhuvmk/aispend/internal/store"
)

// newDebugCmd groups commands that exist for development. Hidden, because they
// are not part of the tool's surface and listing them in help would invite
// questions a prospect should never have to ask.
func newDebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "debug",
		Short:  "Development helpers (hidden)",
		Hidden: true,
	}
	cmd.AddCommand(newDebugSeedCmd())
	cmd.AddCommand(newDebugPanicCmd())
	return cmd
}

func newDebugSeedCmd() *cobra.Command {
	var envelope bool

	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Write synthetic facts, to exercise the pipeline with no network",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			facts := syntheticFacts()

			if envelope {
				return printEnvelope(cmd, facts)
			}

			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()

			s := sink.NewSQLite(db.SQL())
			if err := s.Write(cmd.Context(), facts); err != nil {
				return err
			}
			if err := s.Flush(cmd.Context()); err != nil {
				return err
			}

			h, err := db.Health()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  seeded %d facts %s %d in the database\n",
				len(facts), capsFor(cmd).Sep(), h.Facts)
			return nil
		},
	}

	cmd.Flags().BoolVar(&envelope, "envelope", false,
		"print the versioned fact envelope instead of writing to the database")
	return cmd
}

func printEnvelope(cmd *cobra.Command, facts []fact.Fact) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	id, err := db.InstallID()
	if err != nil {
		return err
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(fact.Envelope{
		Schema:    fact.EnvelopeSchema,
		Agent:     buildinfo.Version,
		InstallID: id,
		SentAt:    time.Now().UTC(),
		Facts:     facts,
	})
}

// syntheticFacts are fixed, not random: seeding twice must produce exactly the
// same three facts, which is what makes the primary key's dedupe observable.
func syntheticFacts() []fact.Fact {
	collected := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	return []fact.Fact{
		{
			Vendor: "openai", Day: "2026-08-27",
			WorkspaceRef: "proj_a91f", PrincipalRef: "sk-test-a4f2", ModelRef: "gpt-5.2",
			InputUnits: 1_200_000, OutputUnits: 84_000, CachedUnits: 310_000,
			UnitKind: "token", AmountMicros: 41_200_000,
			AmountBasis: fact.BasisVendorReported, Revision: 1, CollectedAt: collected,
		},
		{
			Vendor: "anthropic", Day: "2026-08-27",
			WorkspaceRef: "wrkspc_22", PrincipalRef: "sk-test-9c01", ModelRef: "claude-opus-4-6",
			InputUnits: 420_000, OutputUnits: 61_000, CachedUnits: 1_100_000,
			UnitKind: "token", AmountMicros: 128_940_000,
			AmountBasis: fact.BasisVendorReported, Revision: 1, CollectedAt: collected,
		},
		{
			// No workspace and no principal: the vendor did not report those
			// dimensions. Stored as empty strings so re-collection still
			// deduplicates, and rendered as an em dash rather than a zero.
			Vendor: "openrouter", Day: "2026-08-26",
			ModelRef:   "claude-sonnet-4-6",
			InputUnits: 88_000, OutputUnits: 12_400,
			UnitKind: "token", AmountMicros: 2_480_000,
			AmountBasis: fact.BasisComputed, PriceVersion: "2026.08",
			Revision: 1, CollectedAt: collected,
		},
	}
}

// openDB resolves the state directory, creates it if needed, and opens the
// database. Every command that touches storage goes through here.
func openDB() (*store.DB, error) {
	paths, err := resolvePaths()
	if err != nil {
		return nil, err
	}
	return store.Open(paths.DB)
}

// newDebugPanicCmd panics while holding a credential, so the redacting writer
// can be demonstrated rather than merely asserted. A test that only checks the
// regex proves the regex works; this proves the wiring does.
func newDebugPanicCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "panic",
		Short:  "Panic while holding a credential, to exercise the redacting writer",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			type holder struct {
				Vendor string
				Key    string
			}
			h := holder{Vendor: "openai", Key: firstCredential()}
			panic(fmt.Sprintf("deliberate panic while holding %+v", h))
		},
	}
}

// firstCredential returns whatever credential is configured, so the planted
// value in a test is a real one rather than a literal in the source.
func firstCredential() string {
	for _, c := range cred.ResolveAll() {
		if !c.Empty() {
			return c.Secret()
		}
	}
	return "sk-none-configured"
}
