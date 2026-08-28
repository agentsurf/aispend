package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/collect"
	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/store"
)

func newConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "connect <vendor>",
		Short: "Store a vendor credential in your OS keychain",
		Long: `connect takes a key, checks it against the vendor, and stores it in your
operating system's credential store.

The key is never written to the database, a config file, or any output. It is
verified before it is stored, so a key that does not work is refused rather than
saved for you to discover later.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vendor, ok := catalog.Get(args[0])
			if !ok {
				return unknownVendor(args[0])
			}

			out, caps := cmd.OutOrStdout(), capsFor(cmd)

			secret, err := readSecret(cmd, vendor)
			if err != nil {
				return err
			}

			c := cred.New(vendor.ID, cred.SourceKeyring, cred.KeyringRef(vendor.ID), secret)

			// Verify first. Storing an unverified key means the user finds out
			// it was wrong at the least convenient moment, having already been
			// told it worked.
			fmt.Fprintf(out, "\n  checking the key with %s…\n", vendor.Name)
			registry := collect.New(httpClient())
			collector, ok := registry.Get(vendor.ID)
			if !ok {
				return fmt.Errorf("%s has no collector in this build", vendor.ID)
			}

			info, err := collector.Verify(cmd.Context(), c)
			if err != nil {
				var ve *collect.VendorError
				if errors.As(err, &ve) {
					fmt.Fprintf(out, "\n  %s %s\n  %s\n", caps.Fail(), ve.What, wrapIndent(ve.Why, 74, "  "))
					if ve.Fix != "" {
						fmt.Fprintf(out, "\n  Fix:  %s\n", ve.Fix)
					}
					fmt.Fprintf(out, "\n  %s\n", caps.Dim("nothing was saved"))
					return errSilent
				}
				return err
			}

			if err := cred.Store(vendor.ID, secret); err != nil {
				return err
			}

			// The database records that a connection exists and where to look
			// it up. It never records the secret.
			if err := recordConnection(vendor.ID, info, c); err != nil {
				return err
			}

			fmt.Fprintf(out, "\n  %s %s connected %s %s\n", caps.OK(), vendor.Name,
				caps.Sep(), strings.Join(info.Details, " "+caps.Sep()+" "))
			fmt.Fprintf(out, "  %s\n\n  %s\n", caps.Dim("key stored in your OS keychain, not in aispend's database"),
				caps.Dim("Next  aispend scan"))
			return nil
		},
	}
}

// errSilent ends the command with a non-zero exit without cobra printing a
// second error line under a message that has already explained itself.
var errSilent = errors.New("")

// readSecret prompts for a key without echoing it.
func readSecret(cmd *cobra.Command, vendor catalog.Vendor) (string, error) {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "\n  %s needs a %s\n", vendor.Name, vendor.Credential.Kind)
	if vendor.Credential.Where != "" {
		fmt.Fprintf(out, "  Create one at: %s\n", vendor.Credential.Where)
	}
	if vendor.Credential.Note != "" {
		fmt.Fprintf(out, "  %s\n", vendor.Credential.Note)
	}
	fmt.Fprintf(out, "\n  Paste it here (input is hidden): ")

	stdin := int(os.Stdin.Fd())
	if !term.IsTerminal(stdin) {
		// Reading a key from a pipe would put it in a shell history or a CI
		// log. Refuse, and point at the environment variable, which is the
		// right tool for that job.
		fmt.Fprintln(out)
		return "", fmt.Errorf(
			"connect needs a terminal so the key is not echoed\n\n  For scripts, set %s instead",
			strings.Join(vendor.Credential.Env, " or "))
	}

	raw, err := term.ReadPassword(stdin)
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("could not read the key: %w", err)
	}

	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return "", errors.New("no key entered")
	}
	return secret, nil
}

// recordConnection stores what is known about a connection — never the secret.
func recordConnection(vendor string, info collect.AccountInfo, c cred.Credential) error {
	paths, err := resolvePaths()
	if err != nil {
		return err
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		return err
	}
	defer db.Close()

	return db.SaveConnection(store.Connection{
		Vendor:      vendor,
		AccountRef:  info.AccountRef,
		Label:       info.Label,
		CredSource:  string(c.Source),
		KeyringRef:  c.Ref,
		ConnectedAt: time.Now().UTC().Unix(),
		LastOKAt:    time.Now().UTC().Unix(),
	})
}

func unknownVendor(name string) error {
	var names []string
	for _, v := range catalog.Vendors() {
		names = append(names, v.ID)
	}
	return fmt.Errorf("unknown vendor %q\n\n  Known vendors: %s", name, strings.Join(names, ", "))
}

func newDisconnectCmd() *cobra.Command {
	var keepData bool
	var dropData bool

	cmd := &cobra.Command{
		Use:   "disconnect <vendor>",
		Short: "Remove a vendor credential from your OS keychain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			vendor, ok := catalog.Get(args[0])
			if !ok {
				return unknownVendor(args[0])
			}
			out, caps := cmd.OutOrStdout(), capsFor(cmd)

			removed, err := cred.Delete(vendor.ID)
			if err != nil {
				return err
			}

			paths, err := resolvePaths()
			if err != nil {
				return err
			}
			db, err := store.Open(paths.DB)
			if err != nil {
				return err
			}
			defer db.Close()

			if err := db.DeleteConnection(vendor.ID); err != nil {
				return err
			}

			switch {
			case removed:
				fmt.Fprintf(out, "\n  %s removed %s's key from your OS keychain\n", caps.OK(), vendor.Name)
			default:
				fmt.Fprintf(out, "\n  %s %s had no key stored\n", caps.Dash(), vendor.Name)
			}

			// The collected data is a separate decision from the credential,
			// and it is the user's. Deleting it silently would destroy history
			// someone may still want; keeping it silently would surprise
			// someone who expected disconnect to mean gone.
			facts, err := db.VendorFactCount(vendor.ID)
			if err != nil {
				return err
			}
			if facts == 0 {
				return nil
			}

			switch {
			case dropData:
				n, err := db.DeleteVendorFacts(vendor.ID)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "  %s deleted %d collected %s\n", caps.OK(), n, plural(n, "fact", "facts"))
			case keepData:
				fmt.Fprintf(out, "  %s\n", caps.Dim(fmt.Sprintf("kept %d collected facts", facts)))
			default:
				fmt.Fprintf(out, "\n  %d collected %s remain in the database.\n", facts, plural(facts, "fact", "facts"))
				fmt.Fprintf(out, "  %s\n", caps.Dim(
					"aispend disconnect "+vendor.ID+" --drop-data   to delete them"))
				fmt.Fprintf(out, "  %s\n", caps.Dim(
					"aispend purge                       to delete everything"))
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&keepData, "keep-data", false, "keep collected data without being asked")
	cmd.Flags().BoolVar(&dropData, "drop-data", false, "also delete this vendor's collected data")
	return cmd
}
