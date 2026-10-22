package attention

import (
	"slices"
	"strings"
	"testing"
	"time"

	"herdmux/internal/state"
	"herdmux/internal/tmuxtest"
	"herdmux/internal/ui/style"
)

func TestOrder(t *testing.T) {
	t.Parallel()
	agents := []state.Agent{
		{Pane: "%1", Attention: state.Done, Since: 100},
		{Pane: "%2", Attention: state.NeedsInput, Since: 300},
		{Pane: "%3"}, // no Attention
		{Pane: "%4", Attention: state.NeedsInput, Since: 200},
		{Pane: "%5", Attention: state.Done, Since: 50},
		{Pane: "%6", Attention: state.NeedsInput, Since: 200}, // ties keep tmux order
	}
	var got []string
	for _, a := range Order(agents) {
		got = append(got, a.Pane)
	}
	if want := []string{"%4", "%6", "%2", "%5", "%1"}; !slices.Equal(got, want) {
		t.Fatalf("Order = %v, want %v", got, want)
	}
	if len(Order([]state.Agent{{Pane: "%1"}})) != 0 {
		t.Fatal("agents without Attention were kept")
	}
}

func TestPick(t *testing.T) {
	t.Parallel()
	a := Client{Name: "a", Session: "s1", Activity: 10}
	b := Client{Name: "b", Session: "s2", Activity: 20}
	cases := []struct {
		name, pick, lookedAt string
		cs                   []Client
	}{
		{"none focused: most recently active, nothing looked at", "b", "", []Client{a, b}},
		{"focused wins over activity", "a", "s1", []Client{{Name: "a", Session: "s1", Activity: 10, Focused: true}, b}},
		{"several focused: most active", "b", "s2", []Client{{Name: "a", Session: "s1", Activity: 10, Focused: true},
			{Name: "b", Session: "s2", Activity: 20, Focused: true}}},
	}
	for _, c := range cases {
		p, la, ok := Pick(c.cs)
		if !ok || p.Name != c.pick || la != c.lookedAt {
			t.Errorf("%s: got %q %q %v", c.name, p.Name, la, ok)
		}
	}
	if _, _, ok := Pick(nil); ok {
		t.Error("Pick(nil) picked a client")
	}
}

func TestMessage(t *testing.T) {
	t.Parallel()
	got := Message([]string{"api", "w#b"}, map[string]string{"api": state.NeedsInput, "w#b": state.Done})
	want := "#[fg=#{?#{@herdmux_c_attention_needs_input},#{@herdmux_c_attention_needs_input},default}]" + style.IconNeedsInput +
		"#[default] api  #[fg=#{?#{@herdmux_c_attention_done},#{@herdmux_c_attention_done},default}]" + style.IconDone + "#[default] w##b"
	if got != want {
		t.Fatalf("Message =\n%q\nwant\n%q", got, want)
	}
}

// bels counts BEL bytes a client's terminal got. tmux itself sends none here
// (no pane rings), so every BEL is herdmux's.
func bels(c *tmuxtest.Client) int { return strings.Count(c.Output(), "\a") }

// setup is a server with sessions main (attached by the returned clients)
// and other, focus-events on.
func setup(t *testing.T) *tmuxtest.Server {
	s := tmuxtest.New(t)
	s.Tmux("set", "-g", "focus-events", "on")
	s.Tmux("new-session", "-d", "-s", "other")
	s.Tmux("new-session", "-d", "-s", "third")
	return s
}

func raised(session, kind string) state.Outcome {
	return state.Outcome{Raised: true, Session: session, Attention: kind}
}

func ringOnce(t *testing.T, s *tmuxtest.Server, os ...state.Outcome) {
	t.Helper()
	var b Bell
	for _, o := range os {
		b.Add(o)
	}
	if err := b.Ring(s.Client); err != nil {
		t.Fatal(err)
	}
}

// waitBELs waits for want BELs on c, then checks no more arrive.
func waitBELs(t *testing.T, s *tmuxtest.Server, c *tmuxtest.Client, want int) {
	t.Helper()
	if want > 0 {
		s.Eventually(2*time.Second, "the bell", func() bool { return bels(c) >= want })
	}
	time.Sleep(200 * time.Millisecond)
	if got := bels(c); got != want {
		t.Fatalf("client %s got %d BELs, want %d", c.TTY, got, want)
	}
}

func TestBellRingsFocusedClientOnly(t *testing.T) {
	t.Parallel()
	s := setup(t)
	focused := s.Attach("main")
	idle := s.Attach("third")
	idle.FocusOut()
	s.Eventually(2*time.Second, "focus-out", func() bool { return !strings.Contains(idle.Format("#{client_flags}"), "focused") })

	ringOnce(t, s, raised("other", state.Done))
	waitBELs(t, s, focused, 1)
	waitBELs(t, s, idle, 0)
	s.Eventually(2*time.Second, "the message", func() bool {
		return strings.Contains(focused.Output(), style.IconDone+" other")
	})
	if strings.Contains(idle.Output(), style.IconDone) {
		t.Fatal("the unfocused client got the message")
	}

	// A repeat raise rings again.
	ringOnce(t, s, raised("other", state.Done))
	waitBELs(t, s, focused, 2)
}

func TestBellNoFocusRingsMostRecentlyActive(t *testing.T) {
	t.Parallel()
	s := setup(t)
	older := s.Attach("main")
	newer := s.Attach("main")
	older.FocusOut()
	newer.FocusOut()
	// client_activity is in seconds; make newer's input the latest.
	time.Sleep(1100 * time.Millisecond)
	newer.Write("x")
	s.Eventually(2*time.Second, "no client focused", func() bool {
		out := s.Tmux("list-clients", "-F", "#{client_flags}")
		return !strings.Contains(out, "focused")
	})
	s.Eventually(2*time.Second, "newer's activity", func() bool {
		return newer.Format("#{client_activity}") > older.Format("#{client_activity}")
	})

	// With none focused, no session is looked at: even main's raise rings.
	ringOnce(t, s, raised("main", state.NeedsInput))
	waitBELs(t, s, newer, 1)
	waitBELs(t, s, older, 0)
}

func TestBellLookedAtSessionRingsNothing(t *testing.T) {
	t.Parallel()
	s := setup(t)
	c := s.Attach("main")
	ringOnce(t, s, raised("main", state.Done)) // another window of the focused session
	ringOnce(t, s, state.Outcome{Session: "other", Attention: state.Done})
	waitBELs(t, s, c, 0)
}

func TestBellBatchesOneRun(t *testing.T) {
	t.Parallel()
	s := setup(t)
	c := s.Attach("main")
	ringOnce(t, s, raised("other", state.Done), raised("third", state.NeedsInput), raised("other", state.NeedsInput),
		raised("main", state.Done))
	waitBELs(t, s, c, 1)
	want := style.IconNeedsInput + " other  " + style.IconNeedsInput + " third"
	s.Eventually(2*time.Second, "the message", func() bool { return strings.Contains(c.Output(), "third") })
	if !strings.Contains(stripANSI(c.Output()), want) {
		t.Fatalf("message lacks %q:\n%q", want, stripANSI(c.Output()))
	}
	if n := strings.Count(stripANSI(c.Output()), " other"); n != 1 {
		t.Fatalf("other named %d times", n)
	}
}

// stripANSI drops CSI sequences from terminal output.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
