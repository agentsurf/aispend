package config

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A deliberately small TOML subset: comments, [sections], and key = value where
// value is a quoted string, an integer, or a boolean.
//
// The design's §2.4 dependency budget is a real constraint — a go.mod a security
// reviewer opens is part of the UX — and the config schema (§9.2 #4) is four keys
// in two sections. A TOML library would be the sixth direct dependency, added to
// parse less TOML than this file handles. If the schema ever grows past what this
// covers, that's the moment to reconsider, and the error messages below are
// written so a user hits a clear wall rather than a silent misparse.

type tomlValue struct {
	raw  string
	line int
}

// parseTOML returns values keyed "section.key", or "key" at the top level.
func parseTOML(r io.Reader, name string) (map[string]tomlValue, error) {
	out := make(map[string]tomlValue)
	sc := bufio.NewScanner(r)
	section := ""

	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(stripComment(sc.Text()))
		if text == "" {
			continue
		}

		if strings.HasPrefix(text, "[") {
			if !strings.HasSuffix(text, "]") {
				return nil, fmt.Errorf("%s:%d: unterminated section header %q", name, line, text)
			}
			section = strings.TrimSpace(text[1 : len(text)-1])
			if section == "" {
				return nil, fmt.Errorf("%s:%d: empty section name", name, line)
			}
			continue
		}

		key, raw, found := strings.Cut(text, "=")
		if !found {
			return nil, fmt.Errorf("%s:%d: expected 'key = value', got %q", name, line, text)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: missing key before '='", name, line)
		}
		if section != "" {
			key = section + "." + key
		}
		out[key] = tomlValue{raw: strings.TrimSpace(raw), line: line}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return out, nil
}

// stripComment removes a trailing # comment, respecting quoted strings so a
// value like "a # b" survives.
func stripComment(line string) string {
	inQuote := false
	for i, r := range line {
		switch r {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}

func (v tomlValue) asString(name, key string) (string, error) {
	if len(v.raw) >= 2 && strings.HasPrefix(v.raw, `"`) && strings.HasSuffix(v.raw, `"`) {
		s, err := strconv.Unquote(v.raw)
		if err != nil {
			return "", fmt.Errorf("%s:%d: %s is not a valid string: %v", name, v.line, key, err)
		}
		return s, nil
	}
	return "", fmt.Errorf("%s:%d: %s must be a quoted string, got %s", name, v.line, key, v.raw)
}

func (v tomlValue) asBool(name, key string) (bool, error) {
	switch v.raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%s:%d: %s must be true or false, got %s", name, v.line, key, v.raw)
}
