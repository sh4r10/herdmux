package statusbar

import (
	"strings"

	"github.com/sh4r10/herdmux/internal/colorscheme"
	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/ui/style"
)

// The formats are pure: no #(), and nothing but pane options and tmux's own
// variables. Every colour reads a @herdmux_c_<slot> option, so a colorscheme
// apply restyles them without a re-render. Styles are written as separate
// #[...] blocks with no commas, so they can sit inside #{?...} branches.

func ref(slot string) string { return colorscheme.Ref(slot) }

func opt(name string) string { return "#{" + name + "}" }

// icons is one icon (and a space) per agent pane of the window, in pane
// order. Coloured icons use the state slots; plain ones inherit the fg.
func icons(colored bool) string {
	st := opt(state.OptState)
	icon := func(s string) string {
		if colored {
			return "#[fg=" + ref(style.StateSlot(s)) + "]" + style.StateIcon(s) + " "
		}
		return style.StateIcon(s) + " "
	}
	pick := "#{?#{==:" + st + "," + state.Working + "}," + icon(state.Working) +
		",#{?#{==:" + st + "," + state.NeedsInput + "}," + icon(state.NeedsInput) +
		",#{?#{==:" + st + "," + state.Done + "}," + icon(state.Done) +
		"," + icon(state.Unknown) + "}}}"
	return "#{P:#{?" + opt(state.OptAgent) + "," + pick + ",}}"
}

// attention is the window's joined Attention values, one per pane, for #{m:}.
var attention = "#{P:" + opt(state.OptAttention) + " }"

// TabFormat is window-status-format (current false) or
// window-status-current-format (current true): ` <icons> #I:#W `, then the
// zoom icon when zoomed. Attention fills the whole tab (needs-input beats
// done) and draws its icons and name in fg_on_attention; otherwise inactive
// tabs are fg_muted on bg and the current tab is fg on bg_raised, bold.
func TabFormat(current bool) string {
	bg, fg, bold := "bg", "fg_muted", ""
	if current {
		bg, fg, bold = "bg_raised", "fg", "#[bold]"
	}
	body := func(colored bool, fg string) string {
		return " " + icons(colored) + "#[fg=" + fg + "]#I:#W#{?window_zoomed_flag, " + style.IconZoom + ",} "
	}
	fill := func(slot string) string {
		on := ref("fg_on_attention")
		return "#[bg=" + ref(slot) + "]#[fg=" + on + "]" + bold + body(false, on)
	}
	normal := "#[bg=" + ref(bg) + "]#[fg=" + ref(fg) + "]" + bold + body(true, ref(fg))
	return "#{?#{m:*" + state.NeedsInput + "*," + attention + "}," + fill("attention_needs_input") +
		",#{?#{m:*" + state.Done + "*," + attention + "}," + fill("attention_done") +
		"," + normal + "}}#[default]"
}

// StatusLeft is the session name in accent.
func StatusLeft() string {
	return "#[fg=" + ref("accent") + "]#[bold] #S #[default] "
}

// StatusRight is the active pane's directory basename (~ at home), then its
// git branch, a dirty ● and the stale marker …. home is embedded literally
// (tmux formats can't read the environment); init re-runs on every reload.
func StatusRight(home string) string {
	dir := "#{b:pane_current_path}"
	if home != "" {
		dir = "#{?#{==:#{pane_current_path}," + literal(home) + "},~," + dir + "}"
	}
	head, dirty, stale := opt(OptGitHead), opt(OptGitDirty), opt(OptGitStale)
	git := "#{?#{||:" + head + "," + stale + "}, #[fg=" + ref("fg") + "]" + head +
		"#{?" + dirty + ", #[fg=" + ref("state_working") + "]" + DirtyMark + ",}" +
		"#{?" + stale + ",#[fg=" + ref("fg_muted") + "]" + StaleMark + ",},}"
	return "#[fg=" + ref("fg_muted") + "]" + dir + git + " #[default]"
}

// DirtyMark follows the branch when anything is uncommitted; StaleMark
// follows it when the last git run timed out.
const (
	DirtyMark = "●"
	StaleMark = "…"
)

// literal escapes s for use as plain text inside a tmux format conditional.
func literal(s string) string {
	return strings.NewReplacer("#", "##", ",", "#,", "}", "#}").Replace(s)
}
