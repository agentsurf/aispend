package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On almost every run there is no config file, and that is the intended state —
// not a condition worth an error or even a warning.
func TestMissingFileIsNotAnError(t *testing.T) {
	f, err := LoadFile(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if f.Debug != nil || f.NoColor != nil {
		t.Error("absent file produced settings")
	}
}

func TestLoadsEveryKnownSetting(t *testing.T) {
	path := write(t, `
# aispend config
debug    = true
no_color = false

[agent]
interval = "1h"
lookback = "7d"     # trailing re-pull window
endpoint = ""
`)
	f, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if f.Debug == nil || !*f.Debug {
		t.Error("debug not loaded")
	}
	if f.NoColor == nil || *f.NoColor {
		t.Error("no_color=false not loaded")
	}
	if f.Agent.Interval != "1h" || f.Agent.Lookback != "7d" {
		t.Errorf("agent = %+v", f.Agent)
	}
	if f.Agent.Endpoint != "" {
		t.Errorf("endpoint = %q, want empty — no default may enable egress", f.Agent.Endpoint)
	}
}

// "absent" and "set to false" are different facts: only the first should let a
// lower-precedence layer win. The pointer is what preserves the distinction.
func TestAbsentIsNotFalse(t *testing.T) {
	f, err := LoadFile(write(t, "no_color = false\n"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if f.NoColor == nil {
		t.Fatal("no_color = false read as absent")
	}
	if f.Debug != nil {
		t.Error("absent debug read as present")
	}
}

func TestErrorsNameTheFileAndLine(t *testing.T) {
	cases := map[string]string{
		"missing equals":  "debug\n",
		"bad bool":        "debug = yes\n",
		"unknown setting": "colour = true\n",
		"unquoted string": "[agent]\ninterval = 1h\n",
		"bad section":     "[agent\ndebug = true\n",
	}
	for name, body := range cases {
		_, err := LoadFile(write(t, body))
		if err == nil {
			t.Errorf("%s: LoadFile = nil, want an error", name)
			continue
		}
		if !strings.Contains(err.Error(), "config.toml:") {
			t.Errorf("%s: error does not name file and line: %v", name, err)
		}
	}
}

// A typo that silently does nothing teaches people the config file isn't read.
func TestUnknownKeyListsTheKnownOnes(t *testing.T) {
	_, err := LoadFile(write(t, "debgu = true\n"))
	if err == nil {
		t.Fatal("unknown key accepted")
	}
	if !strings.Contains(err.Error(), "agent.endpoint") {
		t.Errorf("error does not list valid settings: %v", err)
	}
}

func TestCommentsAndBlankLinesTolerated(t *testing.T) {
	f, err := LoadFile(write(t, "\n#comment\n\n   # indented\ndebug = true # trailing\n\n"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if f.Debug == nil || !*f.Debug {
		t.Error("debug not loaded past the comments")
	}
}

func TestHashInsideAQuotedStringSurvives(t *testing.T) {
	f, err := LoadFile(write(t, "[agent]\nendpoint = \"https://x.test/#path\"\n"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if f.Agent.Endpoint != "https://x.test/#path" {
		t.Errorf("endpoint = %q, want the # preserved", f.Agent.Endpoint)
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}
