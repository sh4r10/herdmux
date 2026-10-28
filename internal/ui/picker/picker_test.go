package picker

import (
	"errors"
	"flag"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/config"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/ui/launchprompt"
	"github.com/sh4r10/herdmux/internal/ui/split"
	"github.com/sh4r10/herdmux/internal/ui/style"
	"github.com/sh4r10/herdmux/internal/ui/uitest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

var now = time.Unix(1_800_000_000, 0)

func styles() *style.Styles { return style.New(style.PaletteOf(colorscheme.DefaultScheme())) }

// fake is the Backend over a fixed agent list.
type fake struct {
	agents   []state.Agent
	captured []string // panes captured, in order
	saved    []Settings
	stopped  []string
	stopErr  error
}

func (f *fake) Agents() ([]state.Agent, error) { return slices.Clone(f.agents), nil }

func (f *fake) Capture(pane string, n int) []string {
	f.captured = append(f.captured, pane)
	return []string{"\x1b[32m$\x1b[0m echo " + pane, "output of " + pane}
}

func (f *fake) Stop(a state.Agent) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped = append(f.stopped, a.Pane)
	f.agents = slices.DeleteFunc(f.agents, func(b state.Agent) bool { return b.Pane == a.Pane })
	return nil
}

func (f *fake) Save(s Settings) { f.saved = append(f.saved, s) }

func ag(sess string, win int, wname, pane, agent, st, att string, ago int64) state.Agent {
	return state.Agent{Session: sess, WindowIndex: win, WindowName: wname, Pane: pane, Agent: agent,
		State: st, Attention: att, Since: now.Unix() - ago}
}

// agents in tmux's order (sessions by name); the client is on "web".
func agents() []state.Agent {
	return []state.Agent{
		ag("api", 1, "server", "%1", "claude", state.Working, "", 5),
		ag("api", 2, "tests", "%2", "pi", state.Done, state.Done, 300),
		ag("web", 1, "front", "%3", "opencode", state.NeedsInput, state.NeedsInput, 90),
		ag("web", 3, "docs", "%4", "claude", state.Done, "", 7200),
		ag("zeta", 1, "misc", "%5", "pi", state.NeedsInput, state.NeedsInput, 600),
	}
}

func newModel(f *fake, set Settings, p launchprompt.Prompt) Model {
	m := New(Options{Backend: f, Styles: styles(), Session: "web", Settings: set, Prompt: p, Now: func() time.Time { return now }})
	return sized(m, 120, 20)
}

func sized(m Model, w, h int) Model {
	mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return mm.(Model)
}

func press(t *testing.T, m Model, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var mm tea.Model
		mm, cmd = m.Update(uitest.Key(k))
		m = mm.(Model)
	}
	return m, cmd
}

func panes(m Model) string {
	var out []string
	for _, a := range m.Rows() {
		out = append(out, a.Pane)
	}
	return strings.Join(out, " ")
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestOrderByPlaceCurrentSessionFirst(t *testing.T) {
	t.Parallel()
	m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
	if got := panes(m); got != "%3 %4 %1 %2 %5" {
		t.Errorf("by place: %s", got)
	}
	// Windows by index even when tmux lists them otherwise; panes keep tmux's order.
	in := []state.Agent{ag("s", 3, "c", "%9", "pi", "done", "", 1), ag("s", 1, "a", "%7", "pi", "done", "", 1), ag("s", 1, "a", "%8", "pi", "done", "", 1)}
	var got []string
	for _, a := range Order(in, "s", false) {
		got = append(got, a.Pane)
	}
	if strings.Join(got, " ") != "%7 %8 %9" {
		t.Errorf("order: %v", got)
	}
}

func TestScopeToggle(t *testing.T) {
	t.Parallel()
	f := &fake{agents: agents()}
	m := newModel(f, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "j") // on %4 (web)
	m, _ = press(t, m, "tab")
	if got := panes(m); got != "%3 %4" {
		t.Errorf("this session: %s", got)
	}
	if a := m.Rows()[m.Cursor()]; a.Pane != "%4" {
		t.Errorf("selection moved to %s", a.Pane)
	}
	if !strings.Contains(ansi.Strip(m.View()), "this session (web) · by place") {
		t.Errorf("header: %s", ansi.Strip(m.View()))
	}
	m, _ = press(t, m, "tab")
	if got := panes(m); got != "%3 %4 %1 %2 %5" {
		t.Errorf("back to all: %s", got)
	}
	if want := []Settings{{ThisSession: true}, {}}; !slices.Equal(f.saved, want) {
		t.Errorf("saved %v, want %v", f.saved, want)
	}
	// It opens on the remembered scope.
	m = newModel(f, Settings{ThisSession: true}, launchprompt.Prompt{})
	if got := panes(m); got != "%3 %4" {
		t.Errorf("remembered scope: %s", got)
	}
}

func TestSortToggle(t *testing.T) {
	t.Parallel()
	f := &fake{agents: agents()}
	m := newModel(f, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "s")
	// needs-input Attention oldest first (%5 600s, %3 90s), then done (%2), then by place.
	if got := panes(m); got != "%5 %3 %2 %4 %1" {
		t.Errorf("attention first: %s", got)
	}
	if !strings.Contains(ansi.Strip(m.View()), "all sessions · attention first") {
		t.Error("header doesn't say attention first")
	}
	m, _ = press(t, m, "s")
	if got := panes(m); got != "%3 %4 %1 %2 %5" {
		t.Errorf("by place again: %s", got)
	}
	if want := []Settings{{AttentionFirst: true}, {}}; !slices.Equal(f.saved, want) {
		t.Errorf("saved %v, want %v (the persistence hook)", f.saved, want)
	}
	if m = newModel(f, Settings{AttentionFirst: true}, launchprompt.Prompt{}); panes(m) != "%5 %3 %2 %4 %1" {
		t.Errorf("remembered order: %s", panes(m))
	}
}

func TestFilter(t *testing.T) {
	t.Parallel()
	m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "/", "c", "l")
	if got := panes(m); got != "%4 %1" { // agent name
		t.Errorf("filter cl: %s", got)
	}
	if m, _ = press(t, m, "ctrl+n"); m.Cursor() != 1 { // moves while typing
		t.Errorf("ctrl+n while filtering: cursor %d", m.Cursor())
	}
	m, _ = press(t, m, "backspace", "backspace", "a", "p", "i", ":")
	if got := panes(m); got != "%1 %2" { // place
		t.Errorf("filter api: %s", got)
	}
	m, _ = press(t, m, "esc")
	if got := panes(m); got != "%3 %4 %1 %2 %5" {
		t.Errorf("esc clears: %s", got)
	}
	// ⏎ keeps the filter; j/q are then keys again; the first Esc clears it.
	m, _ = press(t, m, "/", "N", "E", "E", "D", "S", "enter")
	if got := panes(m); got != "%3 %5" { // state, case-insensitive
		t.Errorf("filter needs: %s", got)
	}
	m, _ = press(t, m, "j")
	if m.Cursor() != 1 {
		t.Errorf("j after ⏎: cursor %d", m.Cursor())
	}
	m, cmd := press(t, m, "esc")
	if isQuit(cmd) || panes(m) != "%3 %4 %1 %2 %5" {
		t.Errorf("esc on a kept filter: quit=%v rows %s", isQuit(cmd), panes(m))
	}
	if _, cmd := press(t, m, "esc"); !isQuit(cmd) {
		t.Error("second esc doesn't close")
	}
	// q while typing is text.
	m, cmd = press(t, m, "/", "q")
	if isQuit(cmd) || m.filter.Text != "q" {
		t.Errorf("q while typing: quit=%v filter %q", isQuit(cmd), m.filter.Text)
	}
}

func TestMoveAndJump(t *testing.T) {
	t.Parallel()
	f := &fake{agents: agents()}
	m := newModel(f, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "k", "j", "down", "down", "up")
	if m.Cursor() != 2 {
		t.Errorf("cursor %d", m.Cursor())
	}
	// Every move captures the newly selected pane at once.
	if got := f.captured[len(f.captured)-1]; got != "%1" {
		t.Errorf("last capture %s", got)
	}
	m, _ = press(t, m, "j", "j", "j", "j")
	if m.Cursor() != 4 {
		t.Errorf("clamped at the end: %d", m.Cursor())
	}
	m, cmd := press(t, m, "enter")
	if !isQuit(cmd) || m.Result().Jump == nil || m.Result().Jump.Pane != "%5" {
		t.Fatalf("enter: quit=%v result %+v", isQuit(cmd), m.Result())
	}
	if m.View() != "" {
		t.Error("view not blank while closing")
	}
}

func TestTickRefreshes(t *testing.T) {
	t.Parallel()
	f := &fake{agents: agents()}
	m := newModel(f, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "j", "j") // %1
	n := len(f.captured)
	f.agents = append([]state.Agent{ag("web", 0, "new", "%6", "pi", "working", "", 1)}, f.agents...)
	mm, cmd := m.Update(split.Tick{})
	m = mm.(Model)
	if cmd == nil || len(f.captured) != n+1 {
		t.Errorf("tick: next tick %v, captures %d→%d", cmd != nil, n, len(f.captured))
	}
	if a := m.Rows()[m.Cursor()]; a.Pane != "%1" {
		t.Errorf("selection didn't follow its pane: %s", a.Pane)
	}
}

func TestStopConfirmCancelAndClamp(t *testing.T) {
	t.Parallel()
	f := &fake{agents: agents()}
	m := newModel(f, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "x", "n") // any other key cancels
	if len(f.stopped) != 0 || m.mode != modeList {
		t.Fatalf("cancel stopped %v", f.stopped)
	}
	m, _ = press(t, m, "j", "j", "x")
	if !strings.Contains(ansi.Strip(m.View()), " stop claude in api:1 server? (pane stays)") {
		t.Errorf("confirm: %s", ansi.Strip(m.View()))
	}
	m, _ = press(t, m, "y")
	if !slices.Equal(f.stopped, []string{"%1"}) {
		t.Fatalf("stopped %v", f.stopped)
	}
	if m.Cursor() != 2 || m.Rows()[2].Pane != "%2" {
		t.Errorf("cursor %d on %s, want 2 (same row)", m.Cursor(), m.Rows()[m.Cursor()].Pane)
	}
	v := m.View()
	if !strings.Contains(v, uitest.FgSGR(m.st, "state_done")+"stopped claude in api:1 server (pane kept)") {
		t.Errorf("no green stopped message: %s", ansi.Strip(v))
	}
	// The last row: the cursor is clamped to the shorter list.
	m, _ = press(t, m, "j", "j", "x", "y")
	if m.Cursor() != 2 || len(m.Rows()) != 3 {
		t.Errorf("after stopping the last row: cursor %d of %d", m.Cursor(), len(m.Rows()))
	}
	// A failed stop says so in red.
	f.stopErr = errors.New("boom")
	m, _ = press(t, m, "x", "y")
	if v := m.View(); !strings.Contains(v, uitest.FgSGR(m.st, "attention_needs_input")) || !strings.Contains(ansi.Strip(v), "could not stop") {
		t.Errorf("failed stop: %s", ansi.Strip(v))
	}
}

func TestLaunchPromptKeys(t *testing.T) {
	t.Parallel()
	p := launchprompt.Prompt{Agents: config.DefaultAgents()}
	m := newModel(&fake{agents: agents()}, Settings{}, p)
	m, _ = press(t, m, "n")
	if !strings.Contains(ansi.Strip(m.View()), "launch: c claude · d codex · o opencode · p pi · other key cancels") {
		t.Errorf("prompt: %s", ansi.Strip(m.View()))
	}
	m, cmd := press(t, m, "z") // cancels
	if isQuit(cmd) || m.mode != modeList || m.Result().Launch != nil {
		t.Fatal("z didn't just cancel")
	}
	m, cmd = press(t, m, "n", "o")
	if !isQuit(cmd) || m.Result().Launch == nil || m.Result().Launch.Name != "opencode" {
		t.Fatalf("n o: quit=%v %+v", isQuit(cmd), m.Result())
	}
	// q in the prompt cancels and doesn't close.
	m = newModel(&fake{agents: agents()}, Settings{}, p)
	if _, cmd := press(t, m, "n", "q"); isQuit(cmd) {
		t.Error("n q closed the picker")
	}
}

func TestClose(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
		if m, cmd := press(t, m, k); !isQuit(cmd) || m.Result() != (Result{}) {
			t.Errorf("%s: quit=%v result %+v", k, isQuit(cmd), m.Result())
		}
	}
	// a (the opening key's letter) does not close it.
	m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
	if _, cmd := press(t, m, "a"); isQuit(cmd) {
		t.Error("a closed the picker")
	}
}

// checkSize asserts the view is exactly w×h cells.
func checkSize(t *testing.T, v string, w, h int) {
	t.Helper()
	lines := strings.Split(v, "\n")
	if len(lines) != h {
		t.Errorf("%d lines, want %d", len(lines), h)
	}
	for i, l := range lines {
		if ansi.StringWidth(l) != w {
			t.Errorf("line %d is %d wide, want %d: %q", i, ansi.StringWidth(l), w, ansi.Strip(l))
		}
	}
}

func TestViewDefault(t *testing.T) {
	t.Parallel()
	m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
	v := m.View()
	uitest.Golden(t, *update, "default", v)
	checkSize(t, v, 120, 20)
	plain := ansi.Strip(v)
	for _, want := range []string{
		"╭─ agents ─", "all sessions · by place",
		"▌" + style.IconNeedsInput + " opencode  web:1 front", "needs-input   1m", "needs-input  10m",
		style.IconDone + " claude    web:3 docs", "2h",
		style.IconWorking + " claude    api:1 server", "5s",
		// The preview header and the captured lines.
		"│ " + style.IconNeedsInput + " opencode · web:1 front · needs-input 1m", "$ echo %3", "output of %3",
		hints,
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in\n%s", want, plain)
		}
	}
}

func TestViewAttentionRow(t *testing.T) {
	t.Parallel()
	st := styles()
	m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "j") // so the Attention row %3 is unselected
	uitest.Golden(t, *update, "attention", m.View())
	row := m.row(m.Rows()[0], 50, false)
	red := "\x1b[1;" + uitest.FgSGR(st, "attention_needs_input")
	if !strings.HasPrefix(row, " "+red+style.IconNeedsInput+" ") {
		t.Errorf("icon not bold red: %q", row)
	}
	if !strings.Contains(row, red+"opencode") {
		t.Errorf("name not bold red: %q", row)
	}
	if strings.Contains(row, "[48;") {
		t.Errorf("an unselected row paints a background: %q", row)
	}
	// The rest of the row is unchanged: normal fg, not bold.
	if !strings.Contains(row, "\x1b["+uitest.FgSGR(st, "fg")+"  web:1 front") {
		t.Errorf("place not plain fg: %q", row)
	}
	// A done Attention takes attention_done.
	if row := m.row(m.Rows()[3], 50, false); !strings.Contains(row, "\x1b[1;"+uitest.FgSGR(st, "attention_done")+"pi") {
		t.Errorf("done Attention: %q", row)
	}
	// Without Attention the icon takes its state colour and the name fg.
	if row := m.row(m.Rows()[1], 50, false); !strings.Contains(row, uitest.FgSGR(st, "state_done")+style.IconDone) || strings.Contains(row, "\x1b[1;") {
		t.Errorf("plain row: %q", row)
	}
}

func TestViewSelectedRow(t *testing.T) {
	t.Parallel()
	st := styles()
	m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "j")
	uitest.Golden(t, *update, "selected", m.View())
	row := m.row(m.Rows()[1], 50, true)
	if ansi.StringWidth(row) != 50 || !strings.HasPrefix(ansi.Strip(row), "▌") {
		t.Errorf("selected row %q", ansi.Strip(row))
	}
	// Every cell of it is on bg_raised, the bar in accent.
	if !strings.Contains(row, strings.TrimSuffix(uitest.FgSGR(st, "accent"), "m")+";"+uitest.BgSGR(st, "bg_raised")+"▌") {
		t.Errorf("no accent bar: %q", row)
	}
	for i, part := range strings.Split(row, "\x1b[")[1:] {
		if strings.HasPrefix(part, "0m") || strings.HasPrefix(part, "m") {
			continue
		}
		if !strings.Contains(part, uitest.BgSGR(st, "bg_raised")) {
			t.Errorf("segment %d without bg_raised: %q", i, part)
		}
	}
}

func TestViewLongNames(t *testing.T) {
	t.Parallel()
	f := &fake{agents: []state.Agent{
		ag("a-very-long-session-name", 12, "an-extremely-long-window-name-indeed", "%1", "opencode", state.Working, "", 30),
	}}
	m := New(Options{Backend: f, Styles: styles(), Session: "x", Now: func() time.Time { return now }})
	m = sized(m, 100, 12)
	v := m.View()
	uitest.Golden(t, *update, "long-names", v)
	checkSize(t, v, 100, 12)
	if !strings.Contains(ansi.Strip(v), "a-very-long…") {
		t.Errorf("place not cut with …:\n%s", ansi.Strip(v))
	}
}

func TestViewStopConfirm(t *testing.T) {
	t.Parallel()
	m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "x")
	v := m.View()
	uitest.Golden(t, *update, "stop-confirm", v)
	if !strings.Contains(v, uitest.FgSGR(m.st, "attention_needs_input")+" stop opencode in web:1 front? (pane stays)") {
		t.Errorf("confirm not red: %q", v)
	}
}

func TestViewFilterActive(t *testing.T) {
	t.Parallel()
	m := newModel(&fake{agents: agents()}, Settings{}, launchprompt.Prompt{})
	m, _ = press(t, m, "/", "c", "l")
	v := m.View()
	uitest.Golden(t, *update, "filter", v)
	if !strings.Contains(ansi.Strip(v), "> cl█   all sessions · by place") {
		t.Errorf("header:\n%s", ansi.Strip(v))
	}
	m, _ = press(t, m, "a", "x")
	if !strings.Contains(ansi.Strip(m.View()), "no agents match") {
		t.Errorf("no match:\n%s", ansi.Strip(m.View()))
	}
}

func TestViewLaunchConfigError(t *testing.T) {
	t.Parallel()
	cfg, err := config.Parse([]byte("[[agent]]\nname = \"x\"\n"))
	p := launchprompt.Prompt{Agents: cfg.Agents, Err: err}
	m := newModel(&fake{agents: agents()}, Settings{}, p)
	m, _ = press(t, m, "n")
	v := m.View()
	uitest.Golden(t, *update, "launch-config-error", v)
	checkSize(t, v, 120, 20)
	if !strings.Contains(v, uitest.FgSGR(m.st, "attention_needs_input")+"config.toml:") {
		t.Errorf("no red config.toml line:\n%s", ansi.Strip(v))
	}
}

func TestViewEmpty(t *testing.T) {
	t.Parallel()
	m := newModel(&fake{agents: agents()[:2]}, Settings{ThisSession: true}, launchprompt.Prompt{})
	if !strings.Contains(ansi.Strip(m.View()), "no agents in this session · tab for all") {
		t.Errorf("empty session:\n%s", ansi.Strip(m.View()))
	}
	m = newModel(&fake{}, Settings{}, launchprompt.Prompt{})
	checkSize(t, m.View(), 120, 20)
	if m, cmd := press(t, m, "enter", "x", "j"); isQuit(cmd) || m.mode != modeList {
		t.Error("keys on an empty list")
	}
}
