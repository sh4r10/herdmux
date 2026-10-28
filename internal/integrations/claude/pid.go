package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sh4r10/herdmux/internal/state"
)

// maxDepth bounds the walk up the process tree.
const maxDepth = 16

// Find walks up the process tree from pid (the hook's parent; the agent may
// run the hook under `sh -c`) to the agent process, and returns its pid, or
// 0 if no ancestor is that agent. A process is the agent when its argv[0]
// basename (what tmux's #{pane_current_command} shows) or its comm maps to
// agent by state.MatchCommand: "claude", or a version number such as
// "2.1.301" when claude runs from its versioned path; "codex" (the native
// binary, also under the npm wrapper's node process).
func Find(pid int, agent string) int { return find(pid, agent, readProc) }

// procInfo returns a process's names (argv[0] basename, comm) and its parent.
type procInfo func(pid int) (names []string, ppid int, ok bool)

func find(pid int, agent string, info procInfo) int {
	for range maxDepth {
		if pid <= 1 {
			return 0
		}
		names, ppid, ok := info(pid)
		if !ok {
			return 0
		}
		for _, n := range names {
			if state.MatchCommand(n) == agent {
				return pid
			}
		}
		pid = ppid
	}
	return 0
}

func readProc(pid int) ([]string, int, bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	stat, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return nil, 0, false
	}
	// comm is in parentheses and may hold spaces; the fields after the last
	// ')' are: state ppid ...
	s := string(stat)
	i, j := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if i < 0 || j < i {
		return nil, 0, false
	}
	f := strings.Fields(s[j+1:])
	if len(f) < 2 {
		return nil, 0, false
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return nil, 0, false
	}
	names := []string{s[i+1 : j]}
	if cmdline, err := os.ReadFile(dir + "/cmdline"); err == nil && len(cmdline) > 0 {
		argv0, _, _ := bytes.Cut(cmdline, []byte{0})
		names = append(names, filepath.Base(string(argv0)))
	}
	return names, ppid, true
}
