package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repo makes a temp repo on branch "main" with one commit.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	gitIn(t, dir, "add", "a.txt")
	gitIn(t, dir, "commit", "-q", "-m", "one")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, dir string) Status {
	t.Helper()
	st, err := Read(dir, Timeout)
	if err != nil {
		t.Fatalf("Read(%s): %v", dir, err)
	}
	return st
}

func TestClean(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	if st := mustRead(t, dir); st != (Status{Head: "main"}) {
		t.Fatalf("clean: %+v", st)
	}
	// A subdirectory finds the repo by walking up.
	sub := filepath.Join(dir, "x", "y")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if st := mustRead(t, sub); st != (Status{Head: "main"}) {
		t.Fatalf("subdir: %+v", st)
	}
}

func TestDirty(t *testing.T) {
	t.Parallel()
	for name, dirty := range map[string]func(t *testing.T, dir string){
		"modified": func(t *testing.T, dir string) { write(t, filepath.Join(dir, "a.txt"), "b\n") },
		"staged": func(t *testing.T, dir string) {
			write(t, filepath.Join(dir, "b.txt"), "b\n")
			gitIn(t, dir, "add", "b.txt")
		},
		"untracked": func(t *testing.T, dir string) { write(t, filepath.Join(dir, "new.txt"), "n\n") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := repo(t)
			dirty(t, dir)
			if st := mustRead(t, dir); st != (Status{Head: "main", Dirty: true}) {
				t.Fatalf("%s: %+v", name, st)
			}
		})
	}
}

func TestDetached(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	sha := gitIn(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", "--detach")
	if st := mustRead(t, dir); st != (Status{Head: sha[:7]}) {
		t.Fatalf("detached: %+v (sha %s)", st, sha)
	}
}

func TestNewRepoWithoutCommits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "trunk")
	if st := mustRead(t, dir); st != (Status{Head: "trunk"}) {
		t.Fatalf("no commits: %+v", st)
	}
}

func TestNonRepo(t *testing.T) {
	t.Parallel()
	// A fake git that would fail loudly proves git is never run.
	bin := fakeGit(t, "echo ran >&2; exit 1")
	for _, dir := range []string{t.TempDir(), "", "relative/dir"} {
		st, err := read(bin, dir, Timeout)
		if err != nil || st != (Status{}) {
			t.Errorf("non-repo %q: %+v %v", dir, st, err)
		}
	}
}

func fakeGit(t *testing.T, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "git")
	write(t, bin, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestTimeoutKeepsLastValueAndMarksStale(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	slow := fakeGit(t, "sleep 10")
	prev := Status{Head: "main", Dirty: true}

	start := time.Now()
	got := update(slow, prev, dir, 50*time.Millisecond)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("timeout took %v", took)
	}
	if got != (Status{Head: "main", Dirty: true, Stale: true}) {
		t.Fatalf("after timeout: %+v", got)
	}
	// The next successful run clears the stale mark.
	if got = update("git", got, dir, Timeout); got != (Status{Head: "main"}) {
		t.Fatalf("after recovery: %+v", got)
	}
	// A failing git (not a timeout) shows nothing.
	if got = update(fakeGit(t, "exit 128"), prev, dir, Timeout); got != (Status{}) {
		t.Fatalf("after failure: %+v", got)
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	out := "# branch.oid 0123456789abcdef\n# branch.head feature/x\n# branch.upstream origin/feature/x\n# branch.ab +1 -2\n"
	if st := parse(out); st != (Status{Head: "feature/x"}) {
		t.Fatalf("headers only: %+v", st)
	}
	if st := parse(out + "? new.txt\n"); !st.Dirty {
		t.Fatalf("untracked line: %+v", st)
	}
	if st := parse("# branch.oid 0123456789abcdef\n# branch.head (detached)\n"); st.Head != "0123456" {
		t.Fatalf("detached: %+v", st)
	}
}

// A repo's own .git/config must not make the status read run a command:
// herdmux reads every active pane's cwd, including cloned untrusted repos.
func TestRepoFsmonitorDoesNotRun(t *testing.T) {
	t.Parallel()
	dir := repo(t)
	marker := filepath.Join(t.TempDir(), "pwn")
	gitIn(t, dir, "config", "core.fsmonitor", "touch "+marker+"; false")
	write(t, filepath.Join(dir, "a.txt"), "changed\n")
	st := mustRead(t, dir)
	if _, err := os.Stat(marker); err == nil {
		t.Errorf("core.fsmonitor ran (%s exists)", marker)
	}
	if !st.Dirty {
		t.Errorf("status %+v, want dirty", st)
	}
}
