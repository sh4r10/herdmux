// Package install implements `herdmux install` and `herdmux uninstall`: the
// marked block at the end of tmux.conf, the claude hooks, the versioned
// opencode and pi integration files and, with --mcp, the MCP registrations.
// Both change files (and run the agents' own `mcp add/remove`) only; neither
// touches a running tmux server beyond install's read-only PATH check.
//
// Every path comes from the passed-in environment (Resolve). Inside a test
// binary, any path under the real $HOME is refused (guard), and only
// executables under the temp dir are run.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"herdmux/internal/tmux"
)

// Options configure one install or uninstall run.
type Options struct {
	Getenv func(string) string // the environment: HOME, XDG_CONFIG_HOME, PATH, …
	Out    io.Writer           // progress, diffs and the closing advice

	MCP    bool // install: also register the MCP server with each agent
	DryRun bool // install: print a diff of every change and write nothing
	Purge  bool // uninstall: also remove herdmux's config dir

	// Tmux returns a client for the tmux server whose global PATH install
	// checks (the $TMUX server, or tmux's default one outside tmux). A
	// "no server running" error from it means there is nothing to check.
	// nil skips the check.
	Tmux func() (*tmux.Client, error)
}

// Install runs the preflight, then writes every step. It is idempotent.
func Install(o Options) error {
	p, err := Resolve(o.Getenv)
	if err != nil {
		return err
	}
	steps, warn, problems := plan(p, true, o.MCP, false)
	problems = append(problems, preflightInstall(o, steps)...)
	problems = append(problems, preflightFiles(p, steps)...)
	if o.DryRun {
		dryRun(o.Out, p, steps)
		printWarnings(o.Out, warn)
		if len(problems) > 0 {
			return refused("install would refuse", problems)
		}
		fmt.Fprintln(o.Out, "\n(--dry-run: nothing was written)")
		return nil
	}
	if len(problems) > 0 {
		return refused("refusing to install", problems)
	}
	if err := apply(o, p, steps); err != nil {
		return err
	}
	printWarnings(o.Out, warn)
	fmt.Fprintf(o.Out, "\nInstalled. install doesn't touch a running tmux server: apply it with\n"+
		"  tmux source-file %s\n"+
		"or by restarting the server. Agents already running don't have HERDMUX=1 and\n"+
		"show as unknown until you relaunch them.\n", p.tilde(p.TmuxConf))
	return nil
}

// Uninstall reverses exactly what Install wrote.
func Uninstall(o Options) error {
	p, err := Resolve(o.Getenv)
	if err != nil {
		return err
	}
	steps, warn, problems := plan(p, false, true, o.Purge)
	problems = append(problems, preflightCommands(o, steps)...)
	problems = append(problems, preflightFiles(p, steps)...)
	if len(problems) > 0 {
		return refused("refusing to uninstall", problems)
	}
	if err := apply(o, p, steps); err != nil {
		return err
	}
	printWarnings(o.Out, warn)
	fmt.Fprintln(o.Out, "\nUninstalled. A running tmux server keeps HERDMUX=1 and the layer (its keys,\n"+
		"hooks and styles) until you restart it (tmux kill-server): a reload with\n"+
		"tmux source-file doesn't unset what herdmux init set. Undo the hand edits to\n"+
		"tmux.conf (TPM and the plugin lines) yourself.")
	if !o.Purge && exists(p.ConfigDir) {
		fmt.Fprintf(o.Out, "Kept %s (config.toml, the saved colorscheme, your schemes); --purge removes it.\n", p.tilde(p.ConfigDir))
	}
	return nil
}

func refused(what string, problems []string) error {
	return fmt.Errorf("%s; nothing was written:\n  - %s", what, strings.Join(problems, "\n  - "))
}

func printWarnings(w io.Writer, warn []string) {
	for _, s := range warn {
		fmt.Fprintf(w, "warning: %s\n", s)
	}
}

// ---- preflight ----

func preflightInstall(o Options, steps []step) []string {
	var problems []string
	if _, err := commandV(o.Getenv, o.Getenv("PATH")); err != nil {
		problems = append(problems, "herdmux is not on PATH (sh -c 'command -v herdmux' finds nothing); put the herdmux binary in a directory on PATH first (an absolute path, not ~/...)")
	}
	if pr := tmuxPATHProblem(o); pr != "" {
		problems = append(problems, pr)
	}
	return append(problems, preflightCommands(o, steps)...)
}

// preflightCommands checks that each agent CLI a step runs is on PATH.
func preflightCommands(o Options, steps []step) []string {
	var problems []string
	for _, s := range steps {
		if s.argv == nil || !s.changes() {
			continue
		}
		if _, err := lookPath(s.argv[0], o.Getenv("PATH")); err != nil {
			problems = append(problems, fmt.Sprintf("%s is not on PATH, so %q can't run", s.argv[0], strings.Join(s.argv, " ")))
		}
	}
	return problems
}

// preflightFiles checks that each file or directory a step changes can be
// written: the nearest existing directory above it must be writable.
func preflightFiles(p Paths, steps []step) []string {
	var problems []string
	seen := map[string]bool{}
	for _, s := range steps {
		if s.target == "" || !s.changes() {
			continue
		}
		dir := filepath.Dir(realPath(s.target))
		for !exists(dir) && dir != filepath.Dir(dir) {
			dir = filepath.Dir(dir)
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			problems = append(problems, fmt.Sprintf("%s is not a directory", p.tilde(dir)))
		} else if err := syscall.Access(dir, 2 /* W_OK */); err != nil {
			problems = append(problems, fmt.Sprintf("%s is not writable (needed for %s)", p.tilde(dir), p.tilde(s.target)))
		}
	}
	return problems
}

// tmuxPATHProblem checks that a running tmux server would find herdmux:
// `run-shell 'herdmux init'` uses the server's global PATH.
func tmuxPATHProblem(o Options) string {
	if o.Tmux == nil {
		return ""
	}
	c, err := o.Tmux()
	if err != nil {
		return fmt.Sprintf("can't check the tmux server's global PATH: %v", err)
	}
	out, err := c.Run("show-environment", "-g", "PATH")
	if err != nil {
		if msg := err.Error(); strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to") {
			return "" // no server: it will start with the login PATH
		}
		return fmt.Sprintf("can't read the tmux server's global PATH: %v", err)
	}
	path, ok := strings.CutPrefix(out, "PATH=")
	if !ok {
		path = ""
	}
	if _, err := commandV(o.Getenv, path); err != nil {
		return fmt.Sprintf("herdmux is not on the running tmux server's global PATH (%s), which run-shell uses; "+
			"restart the server from a shell that has herdmux on PATH, or run: tmux set-environment -g PATH \"$PATH\"", path)
	}
	return ""
}

// commandV runs `sh -c 'command -v herdmux'` with PATH set to path.
func commandV(getenv func(string) string, path string) (string, error) {
	cmd := exec.Command("/bin/sh", "-c", "command -v herdmux")
	cmd.Env = append(childEnv(getenv, "PATH"), "PATH="+path)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return s, nil
	}
	return "", errors.New("not found")
}

// childEnvKeys are taken from getenv, not the process: everything that
// picks a home, a config dir, a PATH or a tmux server.
var childEnvKeys = []string{"HOME", "PATH", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
	"CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR", "HERDMUX_CONFIG_DIR", "TMUX", "TMUX_PANE"}

// childEnv is the process environment with childEnvKeys replaced by
// getenv's values (dropped when empty), minus the keys in omit.
func childEnv(getenv func(string) string, omit ...string) []string {
	skip := map[string]bool{}
	for _, k := range childEnvKeys {
		skip[k] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !skip[k] {
			env = append(env, kv)
		}
	}
	for _, k := range childEnvKeys {
		if v := getenv(k); v != "" && !containsStr(omit, k) {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// lookPath finds an executable in the given PATH (not the process's).
func lookPath(name, path string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		f := filepath.Join(dir, name)
		if fi, err := os.Stat(f); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return f, nil
		}
	}
	return "", fmt.Errorf("%s: not found on PATH", name)
}

// ---- dry run ----

func dryRun(w io.Writer, p Paths, steps []step) {
	fmt.Fprintln(w, "herdmux install --dry-run: every change install would make.")
	for _, s := range steps {
		label := s.name
		if s.target != "" {
			label += " " + p.tilde(s.target)
		}
		if s.note != "" {
			label += " (" + s.note + ")"
		}
		if !s.changes() {
			fmt.Fprintf(w, "\n%s: %s\n", label, s.past)
			continue
		}
		fmt.Fprintf(w, "\n%s: would %s\n", label, s.action)
		switch {
		case s.argv != nil:
			fmt.Fprintf(w, "  would run: %s\n", strings.Join(s.argv, " "))
		case s.target != "":
			a, b := s.target, s.target
			if !s.exists {
				a = "/dev/null"
			}
			newb := s.new
			if s.remove {
				b, newb = "/dev/null", nil
			}
			fmt.Fprint(w, unifiedDiff(a, b, s.old, newb))
			if s.backup && s.exists && !exists(s.target+bakSuffix) {
				fmt.Fprintf(w, "  would save the original as %s\n", p.tilde(s.target+bakSuffix))
			}
		}
	}
}

// ---- apply ----

func apply(o Options, p Paths, steps []step) error {
	var done []string
	for _, s := range steps {
		label := s.name
		if s.target != "" {
			label += " " + p.tilde(s.target)
		}
		if !s.changes() {
			fmt.Fprintf(o.Out, "%s: %s\n", label, s.past)
			continue
		}
		if err := applyStep(o, p, s); err != nil {
			msg := fmt.Sprintf("%s: %v", label, err)
			if len(done) > 0 {
				msg += "\ndone before it: " + strings.Join(done, "; ")
			} else {
				msg += "\nnothing was changed before it"
			}
			return errors.New(msg + "\nit is safe to fix the problem and run the command again")
		}
		fmt.Fprintf(o.Out, "%s: %s\n", label, s.past)
		done = append(done, s.name)
	}
	return nil
}

func applyStep(o Options, p Paths, s step) error {
	switch {
	case s.argv != nil:
		return runAgentCLI(o.Getenv, p, s.argv)
	case s.removeD:
		if err := guard(s.target); err != nil {
			return err
		}
		return os.RemoveAll(s.target)
	case s.remove:
		if err := guard(s.target); err != nil {
			return err
		}
		if err := os.Remove(s.target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if s.backup && s.exists {
		if err := backup(s.target, s.old, s.perm); err != nil {
			return err
		}
	}
	return writeAtomic(s.target, s.new, s.perm)
}

// runAgentCLI runs claude/pi from the given PATH, in the home directory.
func runAgentCLI(getenv func(string) string, p Paths, argv []string) error {
	bin, err := lookPath(argv[0], getenv("PATH"))
	if err != nil {
		return err
	}
	if testing.Testing() && !within(bin, filepath.Clean(os.TempDir())) {
		return fmt.Errorf("install: test binaries may only run fake agent CLIs under %s, not %s", os.TempDir(), bin)
	}
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Env = childEnv(getenv)
	cmd.Dir = p.Home
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// backup saves path's original bytes as <path>.herdmux.bak, once: an
// existing backup is never overwritten.
func backup(path string, data []byte, perm fs.FileMode) error {
	bak := path + bakSuffix
	if exists(bak) {
		return nil
	}
	return writeAtomic(bak, data, perm)
}

// realPath follows a symlinked file (a dotfiles checkout), so writes
// replace the target and keep the link.
func realPath(path string) string {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		if r, err := filepath.EvalSymlinks(path); err == nil {
			return r
		}
	}
	return path
}

// writeAtomic writes data to a temp file next to path, then renames it over.
func writeAtomic(path string, data []byte, perm fs.FileMode) error {
	if err := guard(path); err != nil {
		return err
	}
	real := realPath(path)
	if err := guard(real); err != nil {
		return err
	}
	dir := filepath.Dir(real)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(real)+".herdmux-tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, real); err != nil {
		return err
	}
	ok = true
	return nil
}
