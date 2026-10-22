package organiser

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"herdmux/internal/tmuxtest"
	"herdmux/internal/ui/split"
)

// Tests of TmuxBackend on throwaway servers, driving the model in-process,
// with the user's base-index 1 and renumber-windows on (herdmux sets
// neither; the test server does).

// server has session main with windows a..e at 1..5 and a focused client
// on c (window 3), whose last window is b (window 2). A pane-focus-in hook
// logs every focus-in into @focus_log (herdmux's Visit hangs off it).
func server(t *testing.T) (*tmuxtest.Server, *tmuxtest.Client, []string, Model) {
	t.Helper()
	s := tmuxtest.New(t)
	s.Tmux("set", "-g", "base-index", "1")
	s.Tmux("set", "-g", "renumber-windows", "on")
	s.Tmux("set", "-g", "focus-events", "on")
	s.Tmux("move-window", "-r", "-t", "main")
	s.Tmux("rename-window", "-t", "main:1", "a")
	for _, n := range []string{"b", "c", "d", "e"} {
		s.Tmux("new-window", "-d", "-t", "main:", "-n", n)
	}
	ids := windowIDs(t, s)
	c := s.Attach("main")
	s.Tmux("select-window", "-t", "main:2")
	s.Tmux("select-window", "-t", "main:3")
	s.Tmux("set-hook", "-g", "pane-focus-in", `set -gaF @focus_log "#{hook_pane} "`)
	m := model(t, s)
	if m.curID != ids[2] || m.lastID != ids[1] || m.Cursor() != 2 {
		t.Fatalf("setup: current %s last %s cursor %d, ids %v", m.curID, m.lastID, m.Cursor(), ids)
	}
	return s, c, ids, m
}

func model(t *testing.T, s *tmuxtest.Server) Model {
	t.Helper()
	b, err := NewTmuxBackend(s.Client, s.Display("main:", "#{session_id}"))
	if err != nil {
		t.Fatal(err)
	}
	if !b.FocusEvents {
		t.Fatal("focus-events not read as on")
	}
	return sized(New(Options{Backend: b, Styles: styles(), Session: "main"}), 120, 20)
}

// windowIDs is the session's window ids by index, after checking that the
// indexes are 1..n with no gaps.
func windowIDs(t *testing.T, s *tmuxtest.Server) []string {
	t.Helper()
	var ids []string
	for i, line := range strings.Split(s.Tmux("list-windows", "-t", "main", "-F", "#{window_index} #{window_id}"), "\n") {
		idx, id, _ := strings.Cut(line, " ")
		if idx != fmt.Sprint(i+1) {
			t.Errorf("indexes not gapless: %q is row %d", line, i+1)
		}
		ids = append(ids, id)
	}
	return ids
}

func current(s *tmuxtest.Server) (cur, last string) {
	for _, line := range strings.Split(s.Tmux("list-windows", "-t", "main", "-F", "#{window_id} #{window_active}#{window_last_flag}"), "\n") {
		id, flags, _ := strings.Cut(line, " ")
		if flags[0] == '1' {
			cur = id
		}
		if flags[1] == '1' {
			last = id
		}
	}
	return cur, last
}

func want(t *testing.T, s *tmuxtest.Server, what string, ids ...string) {
	t.Helper()
	if got := strings.Join(windowIDs(t, s), " "); got != strings.Join(ids, " ") {
		t.Errorf("%s: order %s, want %s", what, got, strings.Join(ids, " "))
	}
}

// Carrying the current window and carrying the last window: indexes stay
// gapless, the current window stays current by id, the last window stays
// last (prefix l still goes there), and no pane sees a focus-in (which
// would be a Visit clearing Attention).
func TestSwapKeepsCurrentAndLastWindow(t *testing.T) {
	t.Parallel()
	s, c, ids, m := server(t)
	a, b, cc, d, e := ids[0], ids[1], ids[2], ids[3], ids[4]

	m, _ = press(m, " ", "j", "j") // c to the end, swapping with d then e
	want(t, s, "carry c down", a, b, d, e, cc)
	m, _ = press(m, "k", " ")
	want(t, s, "carry c up", a, b, d, cc, e)
	if cur, last := current(s); cur != cc || last != b {
		t.Errorf("after carrying the current window: current %s last %s, want %s %s", cur, last, cc, b)
	}
	m, _ = press(m, "g", "g", "j", " ", "j", "j", "j", " ") // b (the last window) to the end
	want(t, s, "carry b", a, d, cc, e, b)
	if cur, last := current(s); cur != cc || last != b {
		t.Errorf("after carrying the last window: current %s last %s, want %s %s", cur, last, cc, b)
	}
	if got := c.Format("#{window_id}"); got != cc {
		t.Errorf("the client is on %s, want %s", got, cc)
	}
	if got := s.Tmux("show", "-gv", "focus-events"); got != "on" {
		t.Errorf("focus-events left %q", got)
	}
	if got, _ := s.TmuxErr("show", "-gv", "@focus_log"); got != "" {
		t.Errorf("focus-in during the swaps: %q", got)
	}
	// prefix l: last-window goes to b, and back.
	s.Tmux("last-window", "-t", "main")
	if got := c.Format("#{window_id}"); got != b {
		t.Errorf("last-window went to %s, want %s", got, b)
	}
	_ = m
}

// Without the fix-up (a bare swap + select-window), the
// re-select does disturb the last window: it becomes the neighbour, which
// now sits in the current window's old slot. This pins the tmux behaviour
// Swap works around.
func TestPlainReselectDisturbsLastWindow(t *testing.T) {
	t.Parallel()
	s, _, ids, _ := server(t)
	s.Tmux("swap-window", "-d", "-s", ids[2], "-t", ids[3], ";", "select-window", "-t", ids[2])
	if cur, last := current(s); cur != ids[2] || last != ids[3] {
		t.Errorf("plain re-select: current %s last %s, want %s %s (tmux changed?)", cur, last, ids[2], ids[3])
	}
}

// Esc and undo put back the original id order with swaps, skipping a
// window killed in the meantime.
func TestEscAndUndoRestoreOrderAfterKill(t *testing.T) {
	t.Parallel()
	s, _, ids, m := server(t)
	a, b, cc, d, e := ids[0], ids[1], ids[2], ids[3], ids[4]

	m, _ = press(m, "g", "g", " ", "j", "j", "j", "j", " ") // a to the end
	want(t, s, "drop", b, cc, d, e, a)
	m, _ = press(m, "g", "g", "j", "j", "x", "y") // kill d
	want(t, s, "kill", b, cc, e, a)
	m, _ = press(m, "u")
	want(t, s, "undo after the kill", a, b, cc, e)
	if len(m.undos) != 0 {
		t.Errorf("undos left: %v", m.undos)
	}

	m, _ = press(m, "G", "x", "y") // kill e, then grab b to the end and Esc
	want(t, s, "second kill", a, b, cc)
	m, _ = press(m, "g", "g", "j", " ", "j", "esc")
	want(t, s, "esc", a, b, cc)
	if cur, last := current(s); cur != cc || last != b {
		t.Errorf("current %s last %s, want %s %s", cur, last, cc, b)
	}
	_ = m
}

// A rename turns automatic-rename off on the window; undo gives the name
// back and unsets it when the window had no local value, or restores the
// local value it had.
func TestUndoRenameRestoresAutomaticRename(t *testing.T) {
	t.Parallel()
	s, _, ids, m := server(t)
	// new-window -n sets a local automatic-rename off; c has none, d has on.
	// Either may then be renamed by tmux (to sh) at any time, so the names
	// to get back are what the field was prefilled with.
	s.Tmux("set", "-wu", "-t", ids[2], "automatic-rename")
	s.Tmux("set", "-w", "-t", ids[3], "automatic-rename", "on")
	local := func(id string) string { return s.Tmux("show", "-wv", "-t", id, "automatic-rename") }
	s.Eventually(3*time.Second, "tmux to rename c and d", func() bool {
		return s.Display(ids[2], "#{window_name}") == "sh" && s.Display(ids[3], "#{window_name}") == "sh"
	})
	mm, _ := m.Update(split.Tick{}) // the model sees the new names
	m = mm.(Model)

	m, _ = press(m, "r")
	oldC := string(m.edit)
	m, _ = pressMsg(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = press(m, "#", "{", "x", "}", ";") // # and ; are plain text
	m, _ = press(m, "enter")
	if got := s.Display(ids[2], "#{window_name}"); got != "#{x};" {
		t.Errorf("renamed to %q", got)
	}
	if got := local(ids[2]); got != "off" {
		t.Errorf("automatic-rename after rename: %q", got)
	}
	// d has automatic-rename on, so tmux may rename it (to sh) at any time;
	// undo gives back the name the rename replaced.
	m, _ = press(m, "j", "r")
	m, _ = pressMsg(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = press(m, "enter", "esc") // an empty name is refused; esc cancels
	if got := local(ids[3]); got != "on" {
		t.Errorf("d's automatic-rename after a cancelled rename: %q", got)
	}
	m, _ = press(m, "r")
	old := string(m.edit)
	m, _ = pressMsg(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = press(m, "d", "2", "enter")
	if got := local(ids[3]); got != "off" || s.Display(ids[3], "#{window_name}") != "d2" {
		t.Errorf("d's rename: automatic-rename %q, name %q", got, s.Display(ids[3], "#{window_name}"))
	}
	m, _ = press(m, "u")
	if got := local(ids[3]); got != "on" || s.Display(ids[3], "#{window_name}") != old {
		t.Errorf("undo d: automatic-rename %q, name %q, want on %q", got, s.Display(ids[3], "#{window_name}"), old)
	}
	m, _ = press(m, "u")
	if got := local(ids[2]); got != "" || s.Display(ids[2], "#{window_name}") != oldC {
		t.Errorf("undo c: automatic-rename %q, name %q, want unset %q", got, s.Display(ids[2], "#{window_name}"), oldC)
	}
	_ = m
}
