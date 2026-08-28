package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prabhuvmk/aispend/internal/config"
	"github.com/prabhuvmk/aispend/internal/ui"
)

func TestReportPathsOnAFreshInstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvHome, dir)

	paths, err := config.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := paths.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	var buf bytes.Buffer
	if err := reportPaths(&buf, paths, ui.Caps{UTF8: true}); err != nil {
		t.Fatalf("reportPaths: %v", err)
	}
	got := buf.String()

	for _, want := range []string{"paths", "config", "0700", "db", "missing", "owners", "optional"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// A database that exists at the wrong mode must be called out, not glossed as
// present. Silent tolerance of 0644 is how a spend file ends up world-readable.
func TestReportPathsFlagsLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvHome, dir)

	paths, _ := config.Resolve()
	if err := os.WriteFile(paths.DB, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(paths.DB, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	var buf bytes.Buffer
	if err := reportPaths(&buf, paths, ui.Caps{UTF8: true}); err != nil {
		t.Fatalf("reportPaths: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "expected 0600") {
		t.Errorf("loose db mode not flagged:\n%s", got)
	}
}

func TestDisplayShortensHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	in := filepath.Join(home, ".aispend", "aispend.db")
	if got := config.Display(in); !strings.HasPrefix(got, "~/") {
		t.Errorf("Display(%q) = %q, want a ~-prefixed path", in, got)
	}
}
