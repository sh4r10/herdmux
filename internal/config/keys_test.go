package config

import (
	"strings"
	"testing"

	"herdmux/internal/tmuxtest"
)

func listKey(s *tmuxtest.Server, table, key string) string {
	out, _ := s.TmuxErr("list-keys", "-T", table, key)
	return out
}

func herdmuxKeys(s *tmuxtest.Server, table string) string {
	var out []string
	for _, l := range strings.Split(s.Tmux("list-keys", "-T", table), "\n") {
		if strings.Contains(l, ourMarker) {
			out = append(out, strings.Join(strings.Fields(l), " "))
		}
	}
	return strings.Join(out, "\n")
}

func keys(edits map[string]string) map[string]string {
	k := DefaultKeys()
	for a, v := range edits {
		k[a] = v
	}
	return k
}

func TestBindDefaults(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	if err := Bind(s.Client, DefaultKeys()); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		`bind-key -T prefix a run-shell -b "herdmux picker --client '#{client_name}'"`,
		`bind-key -T prefix g run-shell -b "herdmux sessionizer --client '#{client_name}'"`,
		`bind-key -T prefix o run-shell -b "herdmux launch --client '#{client_name}'"`,
		`bind-key -T prefix r run-shell -b "herdmux organiser --client '#{client_name}'"`,
		`bind-key -T prefix u run-shell -b "herdmux next-attention --client '#{client_name}'"`,
	}, "\n")
	if got := herdmuxKeys(s, "prefix"); got != want {
		t.Errorf("prefix table:\n%s\nwant:\n%s", got, want)
	}
	if got := herdmuxKeys(s, "root"); got != "" {
		t.Errorf("root table has herdmux keys:\n%s", got)
	}
	if got := s.Tmux("list-keys", "-N", "-T", "prefix", "a"); !strings.Contains(got, "herdmux: open the agent picker") {
		t.Errorf("note %q", got)
	}
}

func TestRebindRestoresTmuxDefault(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	if err := Bind(s.Client, DefaultKeys()); err != nil {
		t.Fatal(err)
	}
	if err := Bind(s.Client, keys(map[string]string{"organiser": "prefix R"})); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "prefix", "r"); got != "bind-key -T prefix r refresh-client" {
		t.Errorf("prefix r is %q, want tmux's refresh-client back", got)
	}
	if got := s.Tmux("list-keys", "-N", "-T", "prefix", "r"); !strings.Contains(got, "Redraw the current client") {
		t.Errorf("prefix r's note %q", got)
	}
	if got := listKey(s, "prefix", "R"); !strings.Contains(got, "herdmux organiser") {
		t.Errorf("prefix R is %q", got)
	}
	// Unbinding launch gives prefix o back to next-pane; a key with nothing
	// before herdmux (prefix a) ends up unbound.
	if err := Bind(s.Client, keys(map[string]string{"organiser": "prefix R", "launch": "", "picker": ""})); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "prefix", "o"); got != "bind-key -T prefix o select-pane -t :.+" {
		t.Errorf("prefix o is %q", got)
	}
	if got := listKey(s, "prefix", "a"); got != "" {
		t.Errorf("prefix a is %q, want unbound", got)
	}
}

func TestRebindRestoresUserBinding(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	// tmux.conf's own binding, with a format and a quote in it.
	s.Tmux("bind-key", "-T", "prefix", "r", "display-message", "#{session_name} 'x'")
	before := listKey(s, "prefix", "r")
	if err := Bind(s.Client, DefaultKeys()); err != nil {
		t.Fatal(err)
	}
	// A tmux.conf reload rebinds prefix r; init runs again and takes it back,
	// remembering the reloaded binding.
	s.Tmux("bind-key", "-T", "prefix", "r", "display-message", "#{session_name} 'y'")
	reloaded := listKey(s, "prefix", "r")
	if err := Bind(s.Client, DefaultKeys()); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "prefix", "r"); !strings.Contains(got, "herdmux organiser") {
		t.Fatalf("prefix r is %q", got)
	}
	if err := Bind(s.Client, keys(map[string]string{"organiser": "M-r"})); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "prefix", "r"); got != reloaded || got == before {
		t.Errorf("prefix r is %q, want %q", got, reloaded)
	}
}

func TestSwapKeys(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	if err := Bind(s.Client, DefaultKeys()); err != nil {
		t.Fatal(err)
	}
	if err := Bind(s.Client, keys(map[string]string{"picker": "prefix r", "organiser": "prefix a"})); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "prefix", "r"); !strings.Contains(got, "herdmux picker") {
		t.Errorf("prefix r is %q", got)
	}
	if got := listKey(s, "prefix", "a"); !strings.Contains(got, "herdmux organiser") {
		t.Errorf("prefix a is %q", got)
	}
	// Back to the defaults, and then everything off: tmux's own keys return.
	if err := Bind(s.Client, DefaultKeys()); err != nil {
		t.Fatal(err)
	}
	if err := Bind(s.Client, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "prefix", "r"); got != "bind-key -T prefix r refresh-client" {
		t.Errorf("prefix r is %q", got)
	}
	if got := herdmuxKeys(s, "prefix"); got != "" {
		t.Errorf("herdmux keys left:\n%s", got)
	}
}

func TestBindRootKeyAndIdempotent(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	k := keys(map[string]string{"colorscheme": "M-c"})
	if err := Bind(s.Client, k); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		return s.Tmux("list-keys") + "\n" + s.Tmux("show-options", "-g")
	}
	first := snapshot()
	if got := listKey(s, "root", "M-c"); !strings.Contains(got, `run-shell -b "herdmux colorscheme --client '#{client_name}'"`) {
		t.Errorf("root M-c is %q", got)
	}
	if err := Bind(s.Client, k); err != nil {
		t.Fatal(err)
	}
	if second := snapshot(); second != first {
		t.Errorf("a second Bind changed things:\n%s\n---\n%s", first, second)
	}
	// Moving it back to unbound restores the root table.
	if err := Bind(s.Client, DefaultKeys()); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "root", "M-c"); got != "" {
		t.Errorf("root M-c is %q", got)
	}
}

func TestBindSemicolonKey(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	if err := Bind(s.Client, keys(map[string]string{"colorscheme": "prefix ;"})); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "prefix", `\;`); !strings.Contains(got, "herdmux colorscheme") {
		t.Errorf("prefix ; is %q", got)
	}
	if err := Bind(s.Client, DefaultKeys()); err != nil {
		t.Fatal(err)
	}
	if got := listKey(s, "prefix", `\;`); got != `bind-key -T prefix \; last-pane` {
		t.Errorf("prefix ; is %q", got)
	}
}
