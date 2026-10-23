package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/sh4r10/herdmux/internal/integrations"
	"github.com/sh4r10/herdmux/internal/integrations/claude"
	"github.com/sh4r10/herdmux/internal/mcp"
)

// A step is one change install or uninstall makes: a file written or
// removed, a directory removed, or an agent CLI run. Planning reads files
// only; apply does the change.
type step struct {
	name   string // "tmux.conf", "claude hooks", …
	target string // the file or directory ("" for a command)

	// File steps.
	old     []byte
	exists  bool
	new     []byte
	remove  bool // remove the file (new is ignored)
	backup  bool // save <file>.herdmux.bak before the first edit
	perm    fs.FileMode
	removeD bool // remove the directory tree at target (uninstall --purge)

	argv []string // a command step (claude/pi mcp add/remove)

	action string // what it does, imperative ("add the herdmux block"); "" when nothing changes
	past   string // the same, done ("added the herdmux block"), or why nothing changes
	note   string // shown by --dry-run: e.g. the plugin is stale
}

func (s step) changes() bool { return s.action != "" }

func readFile(path string) ([]byte, bool, fs.FileMode, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, 0o644, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	perm := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	return b, true, perm, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// ---- tmux.conf ----

const (
	blockStart = "# >>> herdmux >>>"
	blockEnd   = "# <<< herdmux <<<"
	// Block is what install appends to tmux.conf: one line, since
	// `herdmux init` sets focus-events itself.
	Block = blockStart + "\nrun-shell 'herdmux init'\n" + blockEnd + "\n"
)

// removeBlock removes every marked herdmux block from text.
func removeBlock(text string) (string, bool, error) {
	lines := splitLines(text)
	var out []string
	found := false
	for i := 0; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == blockEnd {
			return "", false, fmt.Errorf("line %d: %q without %q before it", i+1, blockEnd, blockStart)
		}
		if l != blockStart {
			out = append(out, lines[i])
			continue
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != blockEnd {
			j++
		}
		if j == len(lines) {
			return "", false, fmt.Errorf("line %d: %q without %q after it", i+1, blockStart, blockEnd)
		}
		found = true
		i = j
	}
	return strings.Join(out, ""), found, nil
}

var tokyoNight = regexp.MustCompile(`janoamaral/tokyo-night-tmux`)

func planTmuxConf(p Paths, install bool) (step, []string, error) {
	s := step{name: "tmux.conf", target: p.TmuxConf, backup: true}
	b, ok, perm, err := readFile(p.TmuxConf)
	if err != nil {
		return s, nil, err
	}
	s.old, s.exists, s.perm = b, ok, perm
	text := string(b)
	rest, found, err := removeBlock(text)
	if err != nil {
		return s, nil, fmt.Errorf("%s: %w", p.tilde(p.TmuxConf), err)
	}
	var warn []string
	if install {
		for i, l := range splitLines(text) {
			t := strings.TrimSpace(l)
			if !strings.HasPrefix(t, "#") && tokyoNight.MatchString(t) {
				warn = append(warn, fmt.Sprintf("%s:%d still loads janoamaral/tokyo-night-tmux; remove that line (herdmux's colorscheme replaces it)", p.tilde(p.TmuxConf), i+1))
			}
		}
		if rest != "" && !strings.HasSuffix(rest, "\n") {
			rest += "\n"
		}
		s.new = []byte(rest + Block)
		switch {
		case bytes.Equal(s.new, b):
			s.past = "the herdmux block is in place"
		case found:
			s.action, s.past = "move the herdmux block to the end", "moved the herdmux block to the end"
		default:
			s.action, s.past = "add the herdmux block at the end", "added the herdmux block at the end"
		}
		return s, warn, nil
	}
	if !found {
		s.past = "no herdmux block"
		return s, nil, nil
	}
	s.new = []byte(rest)
	s.action, s.past = "remove the herdmux block", "removed the herdmux block"
	if rest == "" && !exists(p.TmuxConf+bakSuffix) {
		// install created the file: nothing else was ever in it.
		s.remove = true
		s.action, s.past = "remove the file install created", "removed the file install created"
	}
	return s, nil, nil
}

// ---- JSON files ----

const bakSuffix = ".herdmux.bak"

// planJSON plans an edit of a JSON file by edit, which changes the parsed
// document and reports whether it did.
func planJSON(name, path string, p Paths, install bool, edit func(*jsonDoc) (bool, error)) (step, error) {
	s := step{name: name, target: path, backup: true}
	b, ok, perm, err := readFile(path)
	if err != nil {
		return s, err
	}
	s.old, s.exists, s.perm = b, ok, perm
	doc, err := parseDoc(b)
	if err != nil {
		return s, fmt.Errorf("%s doesn't parse: %w", p.tilde(path), err)
	}
	changed, err := edit(doc)
	if err != nil {
		return s, fmt.Errorf("%s: %w", p.tilde(path), err)
	}
	if !changed {
		return s, nil
	}
	s.new = doc.encode()
	if !install && len(doc.root.members) == 0 && !exists(path+bakSuffix) {
		s.remove = true // install created it, and nothing else is in it
	}
	return s, nil
}

func parseTree(src []byte) any {
	v, err := toTree(src)
	if err != nil {
		panic(err) // embedded or built here: always valid
	}
	return v
}

func isOurHook(v any) bool {
	h, ok := v.(*object)
	if !ok {
		return false
	}
	c, _ := h.get("command")
	return c == claude.Command
}

func isOurGroup(v any) bool {
	g, ok := v.(*object)
	if !ok {
		return false
	}
	hs, _ := g.get("hooks")
	arr, _ := hs.([]any)
	for _, h := range arr {
		if isOurHook(h) {
			return true
		}
	}
	return false
}

// stripOurHooks removes every hook whose command is claude.Command, and any
// group left with no hooks.
func stripOurHooks(groups []any) ([]any, bool) {
	out := []any{}
	changed := false
	for _, g := range groups {
		o, ok := g.(*object)
		hs, _ := o.get2("hooks")
		arr, isArr := hs.([]any)
		if !ok || !isArr {
			out = append(out, g)
			continue
		}
		kept := []any{}
		for _, h := range arr {
			if !isOurHook(h) {
				kept = append(kept, h)
			}
		}
		if len(kept) == len(arr) {
			out = append(out, g)
			continue
		}
		changed = true
		if len(kept) > 0 {
			o.set("hooks", kept)
			out = append(out, o)
		}
	}
	return out, changed
}

// get2 is get on a possibly nil object.
func (o *object) get2(k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	return o.get(k)
}

func claudeEdit(install, withMCP bool) func(*jsonDoc) (bool, error) {
	return func(d *jsonDoc) (bool, error) {
		changed := false
		want := parseTree(claude.Settings).(*object)
		wantHooks, _ := want.get("hooks")
		if install {
			hooks, err := d.root.child("hooks")
			if err != nil {
				return false, err
			}
			for _, ev := range wantHooks.(*object).members {
				var cur []any
				if v, ok := hooks.get(ev.key); ok {
					if cur, ok = v.([]any); !ok {
						return false, fmt.Errorf("hooks.%s is not an array", ev.key)
					}
				}
				ours := []any{}
				for _, g := range cur {
					if isOurGroup(g) {
						ours = append(ours, g)
					}
				}
				if canonical(ours) == canonical(ev.val) {
					continue
				}
				cur, _ = stripOurHooks(cur)
				hooks.set(ev.key, append(cur, ev.val.([]any)...))
				changed = true
			}
		} else if v, ok := d.root.get("hooks"); ok {
			if hooks, ok := v.(*object); ok {
				for _, ev := range append([]member(nil), hooks.members...) {
					arr, ok := ev.val.([]any)
					if !ok {
						continue
					}
					if arr, c := stripOurHooks(arr); c {
						changed = true
						if len(arr) == 0 {
							hooks.del(ev.key)
						} else {
							hooks.set(ev.key, arr)
						}
					}
				}
				if changed && len(hooks.members) == 0 {
					d.root.del("hooks")
				}
			}
		}
		// The MCP read tools' pre-allows: added with --mcp, always removed.
		if install && !withMCP {
			return changed, nil
		}
		allowed := mcp.ClaudeAllow()
		if install {
			perms, err := d.root.child("permissions")
			if err != nil {
				return false, err
			}
			var allow []any
			if v, ok := perms.get("allow"); ok {
				if allow, ok = v.([]any); !ok {
					return false, errors.New("permissions.allow is not an array")
				}
			}
			n := len(allow)
			for _, a := range allowed {
				if !containsString(allow, a) {
					allow = append(allow, a)
				}
			}
			if len(allow) != n {
				perms.set("allow", allow)
				changed = true
			}
			return changed, nil
		}
		pv, _ := d.root.get("permissions")
		perms, ok := pv.(*object)
		if !ok {
			return changed, nil
		}
		av, _ := perms.get("allow")
		allow, ok := av.([]any)
		if !ok {
			return changed, nil
		}
		kept := []any{}
		for _, a := range allow {
			if s, ok := a.(string); !ok || !containsStr(allowed, s) {
				kept = append(kept, a)
			}
		}
		if len(kept) == len(allow) {
			return changed, nil
		}
		if len(kept) == 0 {
			perms.del("allow")
		} else {
			perms.set("allow", kept)
		}
		if len(perms.members) == 0 {
			d.root.del("permissions")
		}
		return true, nil
	}
}

func containsString(arr []any, s string) bool {
	for _, a := range arr {
		if a == s {
			return true
		}
	}
	return false
}

func containsStr(arr []string, s string) bool {
	for _, a := range arr {
		if a == s {
			return true
		}
	}
	return false
}

// opencodeEntry is the global mcp.herdmux entry (the shape of
// dev/opencode/opencode.json).
func opencodeEntry() *object {
	cmd := []any{}
	for _, c := range mcp.Command {
		cmd = append(cmd, c)
	}
	return &object{members: []member{
		{"type", "local"}, {"command", cmd}, {"environment", &object{}}, {"enabled", true}}}
}

func opencodeEdit(install bool) func(*jsonDoc) (bool, error) {
	return func(d *jsonDoc) (bool, error) {
		if install {
			m, err := d.root.child("mcp")
			if err != nil {
				return false, err
			}
			want := opencodeEntry()
			if cur, ok := m.get(mcp.Name); ok && canonical(cur) == canonical(want) {
				return false, nil
			}
			m.set(mcp.Name, want)
			return true, nil
		}
		v, _ := d.root.get("mcp")
		m, ok := v.(*object)
		if !ok || !m.del(mcp.Name) {
			return false, nil
		}
		if len(m.members) == 0 {
			d.root.del("mcp")
		}
		return true, nil
	}
}

// registered returns path's mcpServers.herdmux entry (nil when the file is
// missing, unreadable or has none).
func registered(path string) *object {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	d, err := parseDoc(b)
	if err != nil {
		return nil
	}
	v, _ := d.root.get("mcpServers")
	servers, _ := v.(*object)
	e, _ := servers.get2(mcp.Name)
	o, _ := e.(*object)
	return o
}

func claudeMCPStep(p Paths, install bool) step {
	s := step{name: "claude MCP"}
	reg := registered(p.ClaudeJSON) != nil
	switch {
	case install && reg:
		s.past = "herdmux is already registered (user scope)"
	case install:
		s.argv = append([]string{"claude", "mcp", "add", "-s", "user", mcp.Name, "--"}, mcp.Command...)
		s.action, s.past = "register herdmux (user scope)", "registered herdmux (user scope)"
	case reg:
		s.argv = []string{"claude", "mcp", "remove", "-s", "user", mcp.Name}
		s.action, s.past = "unregister herdmux", "unregistered herdmux"
	default:
		s.past = "herdmux isn't registered"
	}
	return s
}

// piWanted reports whether pi's entry already matches what install adds.
func piWanted(e *object) bool {
	if e == nil {
		return false
	}
	cmd, _ := e.get("command")
	args, _ := e.get("args")
	exp, _ := e.get("exposure")
	want := []any{}
	for _, c := range mcp.Command[1:] {
		want = append(want, c)
	}
	return cmd == mcp.Command[0] && canonical(args) == canonical(want) && exp == "direct"
}

func piMCPStep(p Paths, install bool) step {
	s := step{name: "pi MCP"}
	e := registered(p.PiMCP)
	switch {
	case install && piWanted(e):
		s.past = "herdmux is already registered"
	case install:
		s.argv = append([]string{"pi", "mcp", "add", mcp.Name, "--exposure", "direct", "--"}, mcp.Command...)
		s.action, s.past = "register herdmux (direct exposure)", "registered herdmux (direct exposure)"
	case e != nil:
		s.argv = []string{"pi", "mcp", "remove", mcp.Name}
		s.action, s.past = "unregister herdmux", "unregistered herdmux"
	default:
		s.past = "herdmux isn't registered"
	}
	return s
}

// ---- integration files ----

var (
	headerID      = regexp.MustCompile(`(?m)^// HERDMUX_INTEGRATION_ID[=:] ?(\S+)\s*$`)
	headerVersion = regexp.MustCompile(`(?m)^// HERDMUX_INTEGRATION_VERSION[=:] ?(\d+)\s*$`)
)

// Header returns an integration file's HERDMUX_INTEGRATION_ID and _VERSION
// (version -1 when missing).
func Header(b []byte) (id string, version int) {
	version = -1
	if m := headerID.FindSubmatch(b); m != nil {
		id = string(m[1])
	}
	if m := headerVersion.FindSubmatch(b); m != nil {
		version, _ = strconv.Atoi(string(m[1]))
	}
	return id, version
}

func planPlugin(name, path string, p Paths, data []byte, install bool) (step, []string, error) {
	s := step{name: name, target: path, new: data, perm: 0o644}
	b, ok, perm, err := readFile(path)
	if err != nil {
		return s, nil, err
	}
	s.old, s.exists = b, ok
	if ok {
		s.perm = perm
	}
	wantID, wantV := Header(data)
	if !ok {
		if install {
			s.action = fmt.Sprintf("write version %d", wantV)
			s.past = "wrote version " + strconv.Itoa(wantV)
		} else {
			s.past = "not installed"
		}
		return s, nil, nil
	}
	id, v := Header(b)
	if id != wantID {
		if install {
			return s, nil, fmt.Errorf("%s exists and isn't herdmux's (no HERDMUX_INTEGRATION_ID %s); move it away first", p.tilde(path), wantID)
		}
		s.past = "left in place: not herdmux's"
		return s, []string{fmt.Sprintf("left %s: it has no HERDMUX_INTEGRATION_ID %s, so it isn't herdmux's", p.tilde(path), wantID)}, nil
	}
	if !install {
		s.remove = true
		s.action, s.past = "remove it", "removed it"
		return s, nil, nil
	}
	switch {
	case bytes.Equal(b, data):
		s.past = fmt.Sprintf("up to date (version %d)", v)
	case v < wantV:
		s.note = fmt.Sprintf("stale: version %d on disk, %d in this herdmux", v, wantV)
		s.action, s.past = fmt.Sprintf("upgrade it to version %d", wantV), fmt.Sprintf("upgraded it to version %d", wantV)
	case v == wantV:
		s.note = fmt.Sprintf("modified on disk (version %d)", v)
		s.action, s.past = "restore it", "restored it"
	default:
		s.note = fmt.Sprintf("newer on disk: version %d, this herdmux has %d", v, wantV)
		s.action, s.past = fmt.Sprintf("downgrade it to version %d", wantV), fmt.Sprintf("downgraded it to version %d", wantV)
	}
	return s, nil, nil
}

// plan returns every step in order, plus warnings and the problems that
// make install refuse. It only reads.
func plan(p Paths, install, withMCP, purge bool) (steps []step, warn, problems []string) {
	add := func(s step, w []string, err error) {
		warn = append(warn, w...)
		if err != nil {
			problems = append(problems, err.Error())
			return
		}
		steps = append(steps, s)
	}
	add(planTmuxConf(p, install))
	what := "the herdmux hooks"
	if withMCP || !install {
		what += " and MCP pre-allows"
	}
	s, err := planJSON("claude hooks", p.ClaudeSettings, p, install, claudeEdit(install, withMCP))
	add(describeJSON(s, install, what), nil, err)
	add(planPlugin("opencode plugin", p.OpencodePlugin, p, integrations.OpencodePlugin, install))
	add(planPlugin("pi extension", p.PiExtension, p, integrations.PiExtension, install))
	if install && !withMCP {
		return steps, warn, problems
	}
	s, err = planJSON("opencode MCP", p.OpencodeJSON, p, install, opencodeEdit(install))
	add(describeJSON(s, install, "the mcp.herdmux entry"), nil, err)
	steps = append(steps, claudeMCPStep(p, install), piMCPStep(p, install))
	if purge && exists(p.ConfigDir) {
		if within(p.Home, p.ConfigDir) || p.ConfigDir == "/" {
			problems = append(problems, fmt.Sprintf("won't purge %s: it contains the home directory", p.ConfigDir))
		} else {
			steps = append(steps, step{name: "herdmux config", target: p.ConfigDir, removeD: true,
				action: "remove it (--purge)", past: "removed it (--purge)"})
		}
	}
	return steps, warn, problems
}

// describeJSON fills in a JSON step's messages from whether it changes.
func describeJSON(s step, install bool, what string) step {
	switch {
	case s.new == nil && !s.remove:
		if install {
			s.past = "up to date"
		} else {
			s.past = "nothing of herdmux's"
		}
	case s.remove:
		s.action, s.past = "remove "+what+", then the file (install created it)", "removed "+what+", then the file (install created it)"
	case install && !s.exists:
		s.action, s.past = "create it with "+what, "created it with "+what
	case install:
		s.action, s.past = "merge "+what, "merged "+what
	default:
		s.action, s.past = "remove "+what, "removed "+what
	}
	return s
}
