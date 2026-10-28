package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procStat is the part of /proc/<pid>/stat herdmux needs.
type procStat struct {
	state byte // R, S, Z, ...
	ppid  int
	pgrp  int
	tpgid int // foreground process group of the process's controlling tty
}

// readStat parses /proc/<pid>/stat. The comm field may hold spaces and
// parentheses, so the fields are read after the last ')'.
func readStat(pid int) (procStat, error) {
	if pid <= 0 {
		return procStat{}, fmt.Errorf("invalid pid %d", pid)
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return procStat{}, errors.New("malformed stat")
	}
	// After ") ": state ppid pgrp session tty_nr tpgid ...
	f := strings.Fields(s[i+1:])
	if len(f) < 6 || len(f[0]) == 0 {
		return procStat{}, errors.New("malformed stat")
	}
	ppid, err0 := strconv.Atoi(f[1])
	pgrp, err1 := strconv.Atoi(f[2])
	tpgid, err2 := strconv.Atoi(f[5])
	if err0 != nil || err1 != nil || err2 != nil {
		return procStat{}, errors.New("malformed stat")
	}
	return procStat{state: f[0][0], ppid: ppid, pgrp: pgrp, tpgid: tpgid}, nil
}

// childNamed reports whether some child of ppid is named name: its comm or
// its argv[0] basename (what tmux's #{pane_current_command} shows).
func childNamed(ppid int, name string) bool {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		i, j := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if i < 0 || j < i || strings.Fields(s[j+1:])[1] != strconv.Itoa(ppid) {
			continue
		}
		if s[i+1:j] == name {
			return true
		}
		if cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
			argv0, _, _ := strings.Cut(string(cmdline), "\x00")
			if filepath.Base(argv0) == name {
				return true
			}
		}
	}
	return false
}

// alive reports whether pid is a running (non-zombie) process.
func alive(pid int) bool {
	st, err := readStat(pid)
	return err == nil && st.state != 'Z' && st.state != 'X'
}

// foregroundPgrp returns the foreground process group of the pane's tty, read
// as the tpgid of the pane's own process (pane_pid), which tmux started on
// that tty. It is the process group tmux reads #{pane_current_command} from.
func foregroundPgrp(panePID int) (int, error) {
	st, err := readStat(panePID)
	if err != nil {
		return 0, err
	}
	if st.state == 'Z' || st.tpgid <= 0 {
		return 0, errors.New("pane process has no foreground group")
	}
	return st.tpgid, nil
}

// inForeground reports whether pid is alive and owns the pane's foreground:
// it is in the tty's foreground process group (so an agent started through a
// wrapper that doesn't exec still counts), or the foreground group's leader
// is its descendant (a shell or agent running a foreground child, e.g. the
// `herdmux report` typed at a shell prompt). A backgrounded or suspended
// agent, or one that exited back to the shell, is not.
func inForeground(pid, panePID int) bool {
	st, err := readStat(pid)
	if err != nil || st.state == 'Z' || st.state == 'X' {
		return false
	}
	fg, err := foregroundPgrp(panePID)
	if err != nil {
		return false
	}
	if st.pgrp == fg {
		return true
	}
	p := fg
	for range 64 {
		if p == pid {
			return true
		}
		if p <= 1 || p == panePID {
			return false
		}
		ps, err := readStat(p)
		if err != nil {
			return false
		}
		p = ps.ppid
	}
	return false
}
