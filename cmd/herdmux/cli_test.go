package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type result struct {
	code           int
	stdout, stderr string
	home           string
}

// runCLI runs the CLI in-process with a fake environment: a temp HOME and
// HERDMUX_CONFIG_DIR, and TMUX/HERDR_* unset unless given in vars.
func runCLI(t *testing.T, vars map[string]string, args ...string) result {
	t.Helper()
	home := t.TempDir()
	envm := map[string]string{"HOME": home, "HERDMUX_CONFIG_DIR": filepath.Join(home, "config")}
	for k, v := range vars {
		envm[k] = v
	}
	var out, errb bytes.Buffer
	code := run(args, &env{
		stdin:  strings.NewReader(""),
		stdout: &out,
		stderr: &errb,
		getenv: func(k string) string { return envm[k] },
	})
	return result{code, out.String(), errb.String(), home}
}

func TestNoArgsPrintsHelp(t *testing.T) {
	t.Parallel()
	r := runCLI(t, nil)
	if r.code != 0 || !strings.Contains(r.stdout, "Usage: herdmux <command>") {
		t.Fatalf("code=%d stdout=%q", r.code, r.stdout)
	}
	for _, v := range verbs {
		if !v.hidden && !strings.Contains(r.stdout, "  "+v.name) {
			t.Errorf("help is missing %q", v.name)
		}
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	r := runCLI(t, nil, "--version")
	if r.code != 0 || r.stdout != "herdmux 1.5.2\n" {
		t.Fatalf("code=%d stdout=%q", r.code, r.stdout)
	}
}

func TestHiddenVerbsOnlyInHelpAll(t *testing.T) {
	t.Parallel()
	def := runCLI(t, nil, "help").stdout
	all := runCLI(t, nil, "help", "--all").stdout
	for _, name := range []string{"hook claude|codex", "tmux-hook <event>"} {
		if strings.Contains(def, name) {
			t.Errorf("default help shows hidden %q", name)
		}
		if !strings.Contains(all, name) {
			t.Errorf("help --all is missing %q", name)
		}
	}
	if h := runCLI(t, nil, "--help").stdout; h != def {
		t.Errorf("--help differs from help")
	}
}

func TestEveryVerbHasHelpOutsideTmux(t *testing.T) {
	t.Parallel()
	for _, v := range verbs {
		r := runCLI(t, nil, v.name, "--help")
		if r.code != 0 || !strings.HasPrefix(r.stdout, "Usage: herdmux "+v.name) {
			t.Errorf("%s --help: code=%d stdout=%q stderr=%q", v.name, r.code, r.stdout, r.stderr)
		}
		if r := runCLI(t, nil, "help", v.name); r.code != 0 || !strings.HasPrefix(r.stdout, "Usage: herdmux "+v.name) {
			t.Errorf("help %s: code=%d stdout=%q", v.name, r.code, r.stdout)
		}
	}
}

func TestTmuxVerbsExit2OutsideTmux(t *testing.T) {
	t.Parallel()
	n := 0
	for _, v := range verbs {
		if !v.needsTmux || v.neverFail {
			continue
		}
		n++
		r := runCLI(t, nil, v.name)
		if r.code != 2 || r.stderr != "herdmux: not inside tmux\n" {
			t.Errorf("%s: code=%d stderr=%q", v.name, r.code, r.stderr)
		}
	}
	if n == 0 {
		t.Fatal("no tmux-needing verbs")
	}
}

func TestNeverFailVerbsExit0AndLog(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"hook", "gemini"}, // `hook claude|codex` without HERDMUX=1 is silent (hook_cli_test.go)
		{"report", "working", "--agent", "claude"},
		{"report", "--bogus-flag"},
		{"tmux-hook", "pane-focus-in"},
	}
	for _, args := range cases {
		for _, vars := range []map[string]string{nil, {"TMUX": "/tmp/tmux-0/herdmux-test-none,1,0", "TMUX_PANE": "%0"}} {
			r := runCLI(t, vars, args...)
			if r.code != 0 || r.stdout != "" || r.stderr != "" {
				t.Errorf("%v: code=%d stdout=%q stderr=%q", args, r.code, r.stdout, r.stderr)
			}
			log, err := os.ReadFile(filepath.Join(r.home, ".local", "state", "herdmux", "log"))
			if err != nil || !strings.Contains(string(log), args[0]) {
				t.Errorf("%v: log not written (%v): %q", args, err, log)
			}
		}
	}
}

func TestNeverFailRecoversPanics(t *testing.T) {
	t.Parallel()
	v := &verb{name: "boom", neverFail: true, define: func(*flag.FlagSet) func(*env, []string) error {
		return func(*env, []string) error { panic("kaboom") }
	}}
	home := t.TempDir()
	code := runVerb(v, nil, &env{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, getenv: func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}})
	log, _ := os.ReadFile(filepath.Join(home, ".local", "state", "herdmux", "log"))
	if code != 0 || !strings.Contains(string(log), "boom: panic: kaboom") {
		t.Fatalf("code=%d log=%q", code, log)
	}
}

func TestUsageErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"nope"}, {"list", "--bogus"}, {"help", "nope"}} {
		if r := runCLI(t, nil, args...); r.code != 2 {
			t.Errorf("%v: code=%d", args, r.code)
		}
	}
}

func TestParseInterspersed(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	agent := fs.String("agent", "", "")
	pos, err := parseInterspersed(fs, []string{"working", "--agent", "pi", "extra", "--", "--agent"})
	if err != nil || *agent != "pi" || strings.Join(pos, ",") != "working,extra,--agent" {
		t.Fatalf("pos=%q agent=%q err=%v", pos, *agent, err)
	}
}
