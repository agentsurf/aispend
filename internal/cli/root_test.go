package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/ui"
)

func boolPtr(b bool) *bool { return &b }

// flag > env > file, and the flag wins even when it was explicitly set to the
// value the file already had — Changed(), not the zero value, decides.
func TestSettingsPrecedence(t *testing.T) {
	cases := []struct {
		name     string
		flag     string // "" = not typed
		env      string
		file     *bool
		wantTrue bool
	}{
		{name: "nothing set", wantTrue: false},
		{name: "file only", file: boolPtr(true), wantTrue: true},
		{name: "env beats file", env: "0", file: boolPtr(true), wantTrue: false},
		{name: "flag beats env", flag: "true", env: "0", wantTrue: true},
		{name: "flag beats file", flag: "false", file: boolPtr(true), wantTrue: false},
		{name: "env only", env: "1", wantTrue: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envDebug, tc.env)

			cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
			cmd.Flags().BoolVar(&flagDebug, "debug", false, "")
			cmd.Flags().BoolVar(&flagNoColor, "no-color", false, "")
			flagDebug, flagNoColor = false, false

			args := []string{}
			if tc.flag != "" {
				args = append(args, "--debug="+tc.flag)
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}

			applySettings(cmd, config.File{Debug: tc.file})

			if flagDebug != tc.wantTrue {
				t.Errorf("debug = %v, want %v", flagDebug, tc.wantTrue)
			}
		})
	}
}

func TestConnectionsListsEveryVendor(t *testing.T) {
	var buf bytes.Buffer
	if err := renderConnections(&buf, ui.Caps{UTF8: true}); err != nil {
		t.Fatalf("renderConnections: %v", err)
	}
	got := buf.String()

	for _, want := range []string{"VENDOR", "UNIT", "STATUS", "openai", "anthropic", "openrouter", "token", "not connected"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("escape codes with colour off:\n%q", got)
	}
}

// The credential column tells a user what to set, which is the first question
// anyone has after running this command.
func TestConnectionsNamesTheCredential(t *testing.T) {
	var buf bytes.Buffer
	if err := renderConnections(&buf, ui.Caps{}); err != nil {
		t.Fatalf("renderConnections: %v", err)
	}
	for _, want := range []string{"OPENAI_ADMIN_KEY", "ANTHROPIC_ADMIN_KEY", "OPENROUTER_API_KEY"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output does not name %s", want)
		}
	}
}

func TestConnectionsShowsAMaskedEnvironmentKey(t *testing.T) {
	const secret = "sk-test-0000000000000000a4f2"
	t.Setenv("OPENAI_ADMIN_KEY", secret)
	t.Setenv("ANTHROPIC_ADMIN_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	var buf bytes.Buffer
	if err := renderConnections(&buf, ui.Caps{UTF8: true}); err != nil {
		t.Fatalf("renderConnections: %v", err)
	}
	got := buf.String()

	if strings.Contains(got, secret) {
		t.Fatalf("the table printed the full key:\n%s", got)
	}
	for _, want := range []string{"key in env", "OPENAI_ADMIN_KEY", "sk-…a4f2", "1 of 3 connected"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// The unconfigured vendors still say what to set.
	if !strings.Contains(got, "set ANTHROPIC_ADMIN_KEY") {
		t.Errorf("output does not tell the user what to set:\n%s", got)
	}
}

func TestDoctorCredentialsBlock(t *testing.T) {
	const secret = "sk-test-0000000000000000a4f2"
	t.Setenv("OPENAI_ADMIN_KEY", secret)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_ADMIN_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	var buf bytes.Buffer
	if err := reportCredentials(&buf, ui.Caps{UTF8: true}); err != nil {
		t.Fatalf("reportCredentials: %v", err)
	}
	got := buf.String()

	if strings.Contains(got, secret) {
		t.Fatalf("doctor printed the full key:\n%s", got)
	}
	for _, want := range []string{"credentials", "openai", "env OPENAI_ADMIN_KEY", "sk-…a4f2"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// A vendor with no key prints an em dash, not a failure and not a zero.
	if !strings.Contains(got, "—") || !strings.Contains(got, "no credential") {
		t.Errorf("unconfigured vendors are not reported as absent:\n%s", got)
	}
}
