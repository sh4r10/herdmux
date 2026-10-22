// Package git reads the git status the status bar shows: the branch (or the
// short sha when HEAD is detached) and whether anything is uncommitted,
// untracked included. Nothing else: no counts, no ahead/behind, no merge
// state.
//
// git runs under a timeout (it takes about 0.8 s on /mnt/c, against about
// 10 ms on Linux paths). Update keeps the last value and marks it stale when
// git times out.
package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Timeout is how long one git status may take before the last value is kept
// and marked stale.
const Timeout = 2 * time.Second

// ErrTimeout is returned by Read when git did not finish in time.
var ErrTimeout = errors.New("git: status timed out")

// Status is what the status bar shows for one directory.
type Status struct {
	Head  string // branch name, or the short sha when detached; "" outside a repo
	Dirty bool   // anything uncommitted, untracked included
	Stale bool   // the last git run timed out; Head and Dirty are older
}

// Read returns the status of dir. Outside a repo (no .git found walking up)
// it returns the zero Status without running git.
func Read(dir string, timeout time.Duration) (Status, error) {
	return read("git", dir, timeout)
}

// Update returns the next status for dir given the previous one: on a
// timeout the previous Head and Dirty stay, marked Stale; on any other git
// failure (a deleted directory, a broken repo) nothing is shown.
func Update(prev Status, dir string, timeout time.Duration) Status {
	return update("git", prev, dir, timeout)
}

func update(bin string, prev Status, dir string, timeout time.Duration) Status {
	st, err := read(bin, dir, timeout)
	return Merge(prev, st, err)
}

// Merge is Update's rule applied to a Read result, for callers that read a
// directory once and apply it to several panes, each with its own previous
// status.
func Merge(prev, st Status, err error) Status {
	switch {
	case errors.Is(err, ErrTimeout):
		prev.Stale = true
		return prev
	case err != nil:
		return Status{}
	}
	return st
}

func read(bin, dir string, timeout time.Duration) (Status, error) {
	if !inRepo(dir) {
		return Status{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// core.fsmonitor in the repo's own .git/config is a command git runs:
	// panes sit in untrusted clones too, so it is forced off.
	cmd := exec.CommandContext(ctx, bin, "--no-optional-locks", "-c", "core.fsmonitor=false", "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	cmd.Dir = dir
	// A killed git can leave children holding stdout; don't wait on them.
	cmd.WaitDelay = 100 * time.Millisecond
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return Status{}, ErrTimeout
	}
	if err != nil {
		return Status{}, err
	}
	return parse(out.String()), nil
}

// inRepo walks up from dir looking for .git (a directory, or a file for
// worktrees and submodules), so non-repo directories never run git.
func inRepo(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		if d == filepath.Dir(d) {
			return false
		}
	}
}

// parse reads `git status --porcelain=v2 --branch` output.
func parse(out string) Status {
	var st Status
	var oid, head string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "# branch.oid "):
			oid = strings.TrimPrefix(line, "# branch.oid ")
		case strings.HasPrefix(line, "# branch.head "):
			head = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# "):
			// other headers (upstream, ahead/behind) are not shown
		default:
			st.Dirty = true // a changed, unmerged or untracked entry
		}
	}
	st.Head = head
	if head == "(detached)" {
		st.Head = oid
		if len(oid) > 7 {
			st.Head = oid[:7]
		}
	}
	return st
}
