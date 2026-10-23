package attention

import (
	"testing"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// flag marks a session's first pane as a claude agent with Attention kind,
// since epoch second since.
func flag(s *tmuxtest.Server, session, kind string, since int) {
	pane := s.Display(session+":", "#{pane_id}")
	s.Tmux("set-option", "-p", "-t", pane, state.OptAgent, "claude", ";",
		"set-option", "-p", "-t", pane, state.OptState, kind, ";",
		"set-option", "-p", "-t", pane, state.OptAttention, kind, ";",
		"set-option", "-p", "-t", pane, state.OptSince, string(rune('0'+since)))
}

func elsewhere(s *tmuxtest.Server, session string) string {
	return s.Display(session+":", "#{"+OptElsewhereKind+"}|#{"+OptElsewhereSession+"}|#{"+OptElsewhereMore+"}")
}

func TestSync(t *testing.T) {
	t.Parallel()
	s := setup(t) // main, other, third
	if err := Sync(s.Client); err != nil {
		t.Fatal(err)
	}
	if got := elsewhere(s, "main"); got != "||" {
		t.Fatalf("with no Attention: %q", got)
	}

	flag(s, "main", state.Done, 1)        // oldest, but only done
	flag(s, "other", state.NeedsInput, 3) // most urgent
	flag(s, "third", state.Done, 2)
	if err := Sync(s.Client); err != nil {
		t.Fatal(err)
	}
	for session, want := range map[string]string{
		"main":  state.NeedsInput + "|other|1", // other first, +third
		"other": state.Done + "|main|1",        // its own flag doesn't count
		"third": state.NeedsInput + "|other|1",
	} {
		if got := elsewhere(s, session); got != want {
			t.Errorf("%s: %q, want %q", session, got, want)
		}
	}

	// A Visit clears other's flag: the counts shrink, and a session with
	// nothing left elsewhere loses all three options.
	if err := state.Visit(s.Client, s.Display("other:", "#{window_id}")); err != nil {
		t.Fatal(err)
	}
	s.Tmux("set-option", "-p", "-u", "-t", s.Display("third:", "#{pane_id}"), state.OptAttention)
	if err := Sync(s.Client); err != nil {
		t.Fatal(err)
	}
	if got := elsewhere(s, "other"); got != state.Done+"|main|" {
		t.Fatalf("other: %q", got)
	}
	if got := elsewhere(s, "main"); got != "||" {
		t.Fatalf("main still has %q", got)
	}
}
