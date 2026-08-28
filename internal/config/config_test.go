package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHonoursEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvHome, dir)

	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Dir != dir {
		t.Errorf("Dir = %q, want %q", p.Dir, dir)
	}
	if want := filepath.Join(dir, "aispend.db"); p.DB != want {
		t.Errorf("DB = %q, want %q", p.DB, want)
	}
}

func TestResolveDefaultsToHomeDotAispend(t *testing.T) {
	t.Setenv(EnvHome, "")

	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	home, _ := os.UserHomeDir()
	if want := filepath.Join(home, ".aispend"); p.Dir != want {
		t.Errorf("Dir = %q, want %q", p.Dir, want)
	}
}

func TestEnsureDirCreatesAt0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	t.Setenv(EnvHome, dir)

	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := p.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != DirPerm {
		t.Errorf("perm = %#o, want %#o", perm, DirPerm)
	}
}

// A directory left readable by the group or the world gets tightened rather
// than merely reported: the fix is free, so there is no reason to only warn.
func TestEnsureDirTightensLoosePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Setenv(EnvHome, dir)

	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := p.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	info, _ := os.Stat(dir)
	if perm := info.Mode().Perm(); perm != DirPerm {
		t.Errorf("perm = %#o, want %#o", perm, DirPerm)
	}
}

func TestEnsureDirRejectsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(EnvHome, path)

	p, _ := Resolve()
	if err := p.EnsureDir(); err == nil {
		t.Fatal("EnsureDir succeeded on a regular file, want error")
	}
}

func TestStatOnMissingPathIsNotAnError(t *testing.T) {
	s, err := Stat(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if s.Exists {
		t.Error("Exists = true for a missing path")
	}
}
