package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/prabhuvmk/aispend/internal/dbg"
)

// File is ~/.aispend/config.toml. v1 barely needs it — the design (§9.2 #4)
// wants it anyway, because a daemon can't be configured by flags and env alone,
// and adding a config file later means changing every invocation path. The
// schema stays near-empty on purpose.
//
// Every field is a pointer: "absent" and "set to false" are different facts, and
// only the first should let a lower-precedence layer win.
type File struct {
	Debug   *bool
	NoColor *bool
	Agent   Agent
}

// Agent is the sidecar posture's configuration. Nothing reads it in v1 — it is
// here so the shape is settled before there are agents in the field.
type Agent struct {
	Interval string // "1h"
	Lookback string // "7d" — trailing re-pull window for vendor restatements
	Endpoint string // "" = local only. No flag or env var can turn this on (§9.4).
}

// known keys, so a typo is an error rather than a setting that silently does
// nothing. A config file that accepts anything teaches users it isn't read.
var knownKeys = map[string]bool{
	"debug":          true,
	"no_color":       true,
	"agent.interval": true,
	"agent.lookback": true,
	"agent.endpoint": true,
}

// LoadFile reads config.toml. A missing file is not an error: on almost every
// run there isn't one, and that is the intended state.
func LoadFile(path string) (File, error) {
	var f File

	fh, err := os.Open(path)
	if os.IsNotExist(err) {
		dbg.Printf("no config file at %s (fine — using defaults)", Display(path))
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("cannot read %s: %w", Display(path), err)
	}
	defer fh.Close()

	name := Display(path)
	values, err := parseTOML(fh, name)
	if err != nil {
		return f, err
	}

	for k, v := range values {
		if !knownKeys[k] {
			return f, fmt.Errorf("%s:%d: unknown setting %q (known: %s)", name, v.line, k, knownList())
		}
	}

	if v, ok := values["debug"]; ok {
		b, err := v.asBool(name, "debug")
		if err != nil {
			return f, err
		}
		f.Debug = &b
	}
	if v, ok := values["no_color"]; ok {
		b, err := v.asBool(name, "no_color")
		if err != nil {
			return f, err
		}
		f.NoColor = &b
	}
	for key, dst := range map[string]*string{
		"agent.interval": &f.Agent.Interval,
		"agent.lookback": &f.Agent.Lookback,
		"agent.endpoint": &f.Agent.Endpoint,
	} {
		if v, ok := values[key]; ok {
			s, err := v.asString(name, key)
			if err != nil {
				return f, err
			}
			*dst = s
		}
	}

	dbg.Printf("loaded config from %s (%d settings)", name, len(values))
	return f, nil
}

func knownList() string {
	keys := make([]string, 0, len(knownKeys))
	for k := range knownKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
