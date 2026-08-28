package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/catalog"
	"github.com/prabhuvmk/aispend/internal/cred"
	"github.com/prabhuvmk/aispend/internal/dbg"
	"github.com/prabhuvmk/aispend/internal/ui"
)

func newConnectionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "connections",
		Aliases: []string{"ls"},
		Short:   "List supported vendors and their connection status",
		Long: `connections shows every vendor this build supports and whether it is connected.

It makes no network calls. The vendor list is compiled into the binary, so it is
the same on every machine running this version.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			return renderConnections(out, ui.Detect(out, flagNoColor))
		},
	}
}

func renderConnections(w io.Writer, caps ui.Caps) error {
	vendors := catalog.Vendors()

	t := ui.NewTable(w, caps, "VENDOR", "UNIT", "STATUS", "CREDENTIAL")
	connected := 0
	for _, v := range vendors {
		c := cred.Resolve(v)
		if !c.Empty() {
			connected++
		}
		status, detail := credentialStatus(v, c)
		t.Row(v.ID, v.UnitKind, status, detail)

		dbg.Printf("%s allows %s %s rate %.1f req/s %s verify %s",
			v.ID, strings.Join(v.AllowedHosts, ", "), caps.Sep(), v.RateLimitRPS,
			caps.Sep(), v.Endpoints["verify"])
	}
	if err := t.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\n  %s\n", caps.Dim(fmt.Sprintf(
		"%d of %d connected %s catalog %s %s set a variable above, or run: aispend connect <vendor>",
		connected, len(vendors), caps.Sep(), catalog.Load().Version, caps.Sep())))
	return nil
}

// credentialStatus describes one vendor's credential for the table.
//
// "not connected" and "key in env" are different facts and print differently;
// neither is ever accompanied by the key itself, only its masked form.
func credentialStatus(v catalog.Vendor, c cred.Credential) (status, detail string) {
	if c.Empty() {
		return "not connected", "set " + strings.Join(v.Credential.Env, " or ")
	}
	return "key in " + string(c.Source), c.Ref + " (" + c.Display() + ")"
}
