package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// install/uninstall through the CLI, on runCLI's temp HOME, with a fake
// herdmux on a temp PATH and a tmuxtest server as $TMUX (the only server
// install's PATH check may reach).
func TestInstallCLI(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdmux"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Tmux("set-environment", "-g", "PATH", bin)
	home := t.TempDir()
	vars := map[string]string{"HOME": home, "PATH": bin, "TMUX": s.SocketPath + "," + s.PID + ",0"}

	r := runCLI(t, vars, "install", "--dry-run")
	if r.code != 0 || !strings.Contains(r.stdout, "+run-shell 'herdmux init'") {
		t.Fatalf("dry run: code=%d stdout=%s stderr=%s", r.code, r.stdout, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
		t.Fatal("--dry-run wrote files")
	}
	if r = runCLI(t, vars, "install"); r.code != 0 {
		t.Fatalf("install: code=%d stderr=%s", r.code, r.stderr)
	}
	conf, err := os.ReadFile(filepath.Join(home, ".config", "tmux", "tmux.conf"))
	if err != nil || !strings.HasSuffix(string(conf), "run-shell 'herdmux init'\n# <<< herdmux <<<\n") {
		t.Fatalf("tmux.conf = %q, %v", conf, err)
	}
	if r = runCLI(t, vars, "uninstall"); r.code != 0 || !strings.Contains(r.stdout, "keeps HERDMUX=1") {
		t.Fatalf("uninstall: code=%d stdout=%s stderr=%s", r.code, r.stdout, r.stderr)
	}

	// Without $TMUX, install's check would need tmux's default server,
	// which a test binary refuses: install refuses too.
	delete(vars, "TMUX")
	if r = runCLI(t, vars, "install"); r.code != 1 || !strings.Contains(r.stderr, "may not use the default server") {
		t.Fatalf("install outside tmux in a test: code=%d stderr=%s", r.code, r.stderr)
	}
	if r = runCLI(t, vars, "install", "extra"); r.code != 2 {
		t.Fatalf("install extra: code=%d", r.code)
	}
}
