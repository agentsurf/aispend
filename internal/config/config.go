// Package config resolves where aispend keeps its state on disk.
//
// Everything lives under one directory so `aispend purge` has exactly one place
// to delete. The directory is 0700 and every file in it is 0600: the database
// holds no secrets, but it does hold a customer's spend, and a world-readable
// file in a home directory is an objection you don't want to answer.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/prabhuvmk/aispend/internal/dbg"
)

const (
	// EnvHome overrides the state directory. Tests set it; so can a user with an
	// unusual home. It is the only knob, on purpose.
	EnvHome = "AISPEND_HOME"

	// DirPerm is the required mode of the state directory.
	DirPerm os.FileMode = 0o700
	// FilePerm is the required mode of every file inside it.
	FilePerm os.FileMode = 0o600
)

// Paths is the resolved set of locations for one invocation.
type Paths struct {
	Dir    string // ~/.aispend
	DB     string // ~/.aispend/aispend.db
	Owners string // ~/.aispend/owners.csv   (optional, user-supplied)
	Config string // ~/.aispend/config.toml  (optional, agent posture)
	Raw    string // ~/.aispend/raw/         (only with --keep-raw)
}

// Resolve computes the paths without touching the filesystem.
func Resolve() (Paths, error) {
	dir := os.Getenv(EnvHome)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("cannot determine home directory: %w", err)
		}
		dir = filepath.Join(home, ".aispend")
	} else {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return Paths{}, fmt.Errorf("cannot resolve %s=%q: %w", EnvHome, dir, err)
		}
		dir = abs
		dbg.Printf("state directory overridden by %s=%s", EnvHome, dir)
	}

	return Paths{
		Dir:    dir,
		DB:     filepath.Join(dir, "aispend.db"),
		Owners: filepath.Join(dir, "owners.csv"),
		Config: filepath.Join(dir, "config.toml"),
		Raw:    filepath.Join(dir, "raw"),
	}, nil
}

// EnsureDir creates the state directory at 0700 if it is missing, and tightens
// the permissions if it exists but is readable by anyone else.
func (p Paths) EnsureDir() error {
	info, err := os.Stat(p.Dir)
	switch {
	case os.IsNotExist(err):
		dbg.Printf("creating %s at %#o", p.Dir, DirPerm)
		if err := os.MkdirAll(p.Dir, DirPerm); err != nil {
			return fmt.Errorf("cannot create %s: %w", p.Dir, err)
		}
		// MkdirAll applies umask, so set the mode explicitly.
		return os.Chmod(p.Dir, DirPerm)
	case err != nil:
		return fmt.Errorf("cannot stat %s: %w", p.Dir, err)
	case !info.IsDir():
		return fmt.Errorf("%s exists but is not a directory", p.Dir)
	}

	if perm := info.Mode().Perm(); perm != DirPerm {
		dbg.Printf("tightening %s from %#o to %#o", p.Dir, perm, DirPerm)
		if err := os.Chmod(p.Dir, DirPerm); err != nil {
			return fmt.Errorf("cannot set %#o on %s: %w", DirPerm, p.Dir, err)
		}
	}
	return nil
}

// Display shortens a path for output: $HOME becomes ~ so reports stay readable
// and don't leak a username into a screenshot a prospect pastes into a ticket.
func Display(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

// State describes what is on disk at one path, for `doctor`.
type State struct {
	Exists bool
	Perm   os.FileMode
	IsDir  bool
}

// Stat reports the state of a path. A missing path is not an error — most of
// them are legitimately absent on a first run.
func Stat(path string) (State, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("cannot stat %s: %w", path, err)
	}
	return State{Exists: true, Perm: info.Mode().Perm(), IsDir: info.IsDir()}, nil
}

// GOOS reports the operating system, so messages can name what the user
// actually sees rather than a generic term.
func GOOS() string { return runtime.GOOS }
