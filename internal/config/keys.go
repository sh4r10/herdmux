package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sh4r10/herdmux/internal/tmux"
)

// Spec is one [keys] value: a tmux key in the prefix or root table. The zero
// Spec is "unbound".
type Spec struct {
	Table string // "prefix" or "root"
	Key   string // a tmux key name, e.g. "a", "M-a", "F5"
}

// ParseSpec parses "prefix <key>" (a prefix key), "<key>" (a root key) or ""
// (unbound). Key names follow tmux: modifiers C-, M-, S- (or ^x) in front of
// one character or a named key such as Enter, F1 or PageUp.
func ParseSpec(s string) (Spec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Spec{}, nil
	}
	sp := Spec{Table: "root", Key: s}
	if k, ok := strings.CutPrefix(s, "prefix "); ok {
		sp = Spec{Table: "prefix", Key: strings.TrimSpace(k)}
	}
	if sp.Key == "" || strings.ContainsAny(sp.Key, " \t") {
		return Spec{}, errors.New(`want "prefix <key>", "<key>" or ""`)
	}
	if !validKey(sp.Key) {
		return Spec{}, fmt.Errorf("%q is not a tmux key name", sp.Key)
	}
	return sp, nil
}

// String is the canonical [keys] form ("prefix a", "M-a" or "").
func (s Spec) String() string {
	switch s.Table {
	case "":
		return ""
	case "prefix":
		return "prefix " + s.Key
	}
	return s.Key
}

// namedKeys are tmux's key names (key-string.c), lowercased: tmux matches
// them case-insensitively.
var namedKeys = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`Enter Escape Space Tab BTab BSpace IC Insert DC Delete Home End
		NPage PageDown PgDn PPage PageUp PgUp Up Down Left Right
		KP/ KP* KP- KP7 KP8 KP9 KP+ KP4 KP5 KP6 KP1 KP2 KP3 KPEnter KP0 KP.`) {
		namedKeys[strings.ToLower(k)] = true
	}
	for i := 1; i <= 24; i++ {
		namedKeys[fmt.Sprintf("f%d", i)] = true
	}
}

func validKey(k string) bool {
	for {
		switch {
		case len(k) > 2 && k[1] == '-' && strings.ContainsRune("CMScms", rune(k[0])):
			k = k[2:]
			continue
		case len(k) > 1 && k[0] == '^':
			k = k[1:]
			continue
		}
		break
	}
	if utf8.RuneCountInString(k) == 1 {
		r, _ := utf8.DecodeRuneInString(k)
		return r > ' ' && r != 0x7f
	}
	return namedKeys[strings.ToLower(k)]
}

// Bookkeeping options for Bind (global, runtime-only). They hold what
// herdmux bound last time, not configuration: [keys] lives only in
// config.toml.
func boundOpt(action string) string   { return "@herdmux_key_" + action }
func restoreOpt(action string) string { return "@herdmux_key_" + action + "_restore" }

// Notes shown by `prefix ?` (list-keys -N).
var notes = map[string]string{
	"picker":         "herdmux: open the agent picker",
	"next-attention": "herdmux: jump to the next agent that needs attention",
	"organiser":      "herdmux: open the window organiser",
	"launch":         "herdmux: launch an agent",
	"sessionizer":    "herdmux: open the sessionizer",
	"colorscheme":    "herdmux: open the colorscheme picker",
}

// BindingCommand is the shell command an action's key runs: the verb with
// the invoking client passed explicitly (a bare run-shell can resolve the
// wrong client). run-shell expands #{client_name} when the key is pressed.
func BindingCommand(action string) string {
	return "herdmux " + action + " --client '#{client_name}'"
}

// ourMarker is how a binding herdmux made shows in list-keys.
const ourMarker = `run-shell -b "herdmux `

// Bind applies keys (action → spec, as in Config.Keys; missing actions are
// unbound) to the server. It is idempotent and runs from `herdmux init` and
// after an MCP write_config:
//
//   - an action whose key moved or was unbound loses herdmux's old binding,
//     and whatever was bound there before herdmux (tmux.conf's binding or
//     tmux's default, e.g. prefix r = refresh-client) is restored;
//   - each bound action gets `run-shell -b 'herdmux <action> --client …'`,
//     remembering the binding it replaced.
//
// A key that tmux refuses is skipped; the others still apply and the error
// names it.
func Bind(c *tmux.Client, keys map[string]string) error {
	vars := make([]string, 0, 2*len(Actions))
	for _, a := range Actions {
		vars = append(vars, "#{"+boundOpt(a)+"}", "#{"+restoreOpt(a)+"}")
	}
	out, err := c.Display("", tmux.Fields(vars...))
	if err != nil {
		return err
	}
	f := tmux.Split(out)
	if len(f) != len(vars) {
		return fmt.Errorf("reading key bookkeeping: got %d fields, want %d", len(f), len(vars))
	}
	prev := map[string]Spec{}
	restore := map[string]string{}
	want := map[string]Spec{}
	for i, a := range Actions {
		prev[a], _ = ParseSpec(f[2*i])
		restore[a] = f[2*i+1]
		if want[a], err = ParseSpec(keys[a]); err != nil {
			return fmt.Errorf("[keys] %s: %v", a, err)
		}
	}

	var errs []error
	// Free every key that moved first, so actions may swap keys.
	for _, a := range Actions {
		p := prev[a]
		if p == (Spec{}) || p == want[a] {
			continue
		}
		if line, _ := binding(c, p); strings.Contains(line, ourMarker) {
			if _, err := c.Run("unbind-key", "-T", p.Table, tmuxKey(p.Key)); err != nil {
				errs = append(errs, err)
			}
			if restore[a] != "" {
				if err := source(c, restore[a]); err != nil {
					errs = append(errs, fmt.Errorf("restoring %s: %w", p, err))
				}
			}
		}
		if _, err := c.Run("set-option", "-gu", boundOpt(a), ";", "set-option", "-gu", restoreOpt(a)); err != nil {
			errs = append(errs, err)
		}
	}
	for _, a := range Actions {
		w := want[a]
		if w == (Spec{}) {
			continue
		}
		old, _ := binding(c, w)
		saved := restore[a]
		if prev[a] != w || !strings.Contains(old, ourMarker) {
			saved = old // tmux.conf (re)bound it since: that is what to restore
			if strings.Contains(old, ourMarker) {
				saved = ""
			}
		}
		if _, err := c.Run("bind-key", "-N", notes[a], "-T", w.Table, tmuxKey(w.Key),
			"run-shell", "-b", BindingCommand(a)); err != nil {
			errs = append(errs, fmt.Errorf("[keys] %s = %q: %w", a, w.String(), err))
			continue
		}
		if _, err := c.Run("set-option", "-g", boundOpt(a), tmux.Literal(w.String()), ";",
			"set-option", "-g", restoreOpt(a), tmux.Literal(saved)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func tmuxKey(k string) string { return tmux.Literal(k) }

// binding returns the bind-key line for a key ("" if unbound), with its note
// (-N) put back, so it can be sourced to restore it later.
func binding(c *tmux.Client, s Spec) (string, error) {
	line, err := c.Run("list-keys", "-T", s.Table, tmuxKey(s.Key))
	if err != nil || !strings.HasPrefix(line, "bind-key ") {
		return "", err
	}
	line, _, _ = strings.Cut(line, "\n")
	if n, err := c.Run("list-keys", "-N", "-T", s.Table, tmuxKey(s.Key)); err == nil {
		// "<key> <note>"
		if _, note, ok := strings.Cut(strings.TrimSpace(n), " "); ok {
			if note = strings.TrimSpace(note); note != "" && !strings.Contains(note, "'") {
				line = "bind-key -N '" + note + "'" + strings.TrimPrefix(line, "bind-key")
			}
		}
	}
	return line, nil
}

// source runs a saved bind-key line through `source-file -`.
func source(c *tmux.Client, line string) error {
	cmd := c.Command("source-file", "-")
	cmd.Stdin = strings.NewReader(line + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
