package ui

import (
	"bytes"
	"strings"
	"testing"
)

// A non-TTY destination must never receive escape codes: the report has to
// survive being piped to a file or pasted into an email.
func TestNoColorWhenNotATTY(t *testing.T) {
	var buf bytes.Buffer
	caps := Detect(&buf, false)
	if caps.Color {
		t.Error("Color = true for a non-TTY writer")
	}
	if got := caps.Dim("x"); got != "x" {
		t.Errorf("Dim added styling to a non-TTY writer: %q", got)
	}
}

func TestNoColorEnvAndFlag(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("NO_COLOR", "1")
	if Detect(&buf, false).Color {
		t.Error("NO_COLOR ignored")
	}
	t.Setenv("NO_COLOR", "")
	if Detect(&buf, true).Color {
		t.Error("--no-color ignored")
	}
}

func TestSymbolsFallBackToASCII(t *testing.T) {
	utf8 := Caps{UTF8: true}
	ascii := Caps{UTF8: false}

	for name, pair := range map[string][2]string{
		"OK":   {utf8.OK(), ascii.OK()},
		"Warn": {utf8.Warn(), ascii.Warn()},
		"Fail": {utf8.Fail(), ascii.Fail()},
		"Dash": {utf8.Dash(), ascii.Dash()},
	} {
		if pair[0] == pair[1] {
			t.Errorf("%s: no ASCII fallback (%q)", name, pair[0])
		}
		for _, r := range pair[1] {
			if r > 127 {
				t.Errorf("%s: ASCII fallback %q is not ASCII", name, pair[1])
			}
		}
	}
}

func TestUTF8LocaleDetection(t *testing.T) {
	cases := map[string]bool{
		"en_US.UTF-8": true,
		"C.utf8":      true,
		"C":           false,
		"POSIX":       false,
	}
	for lang, want := range cases {
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_CTYPE", "")
		t.Setenv("LANG", lang)
		if got := utf8Locale(); got != want {
			t.Errorf("LANG=%s: utf8Locale() = %v, want %v", lang, got, want)
		}
	}
}

func TestTableAligns(t *testing.T) {
	var buf bytes.Buffer
	tbl := NewTable(&buf, Caps{}, "VENDOR", "UNIT", "STATUS")
	tbl.Row("openai", "token", "not connected")
	tbl.Row("a-much-longer-vendor", "token", "connected")
	if err := tbl.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), buf.String())
	}
	// The third column starts at the same offset on every line, which is the
	// only thing "aligned" can be checked to mean.
	want := strings.Index(lines[0], "STATUS")
	for i, cell := range []string{"not connected", "connected"} {
		if got := strings.Index(lines[i+1], cell); got != want {
			t.Errorf("row %d: column starts at %d, header at %d:\n%s", i+1, got, want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Error("table emitted an escape code with colour off")
	}
}

func TestSepFallsBackToASCII(t *testing.T) {
	if got := (Caps{UTF8: false}).Sep(); got != "|" {
		t.Errorf("Sep() = %q on a non-UTF-8 terminal, want ASCII", got)
	}
}

// The whole point of the fallback is that a non-UTF-8 terminal sees no
// multi-byte characters at all, so assert it across every glyph at once rather
// than one at a time and hope the next one added gets a test.
func TestNothingNonASCIIWhenUTF8IsOff(t *testing.T) {
	c := Caps{UTF8: false}
	for name, s := range map[string]string{
		"OK": c.OK(), "Warn": c.Warn(), "Fail": c.Fail(), "Dash": c.Dash(), "Sep": c.Sep(),
	} {
		for _, r := range s {
			if r > 127 {
				t.Errorf("%s() = %q, which is not ASCII", name, s)
			}
		}
	}
}
