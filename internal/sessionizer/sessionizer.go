package sessionizer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"herdmux/internal/state"
	"herdmux/internal/tmux"
)

// Entry is one row in the sessionizer list: either an open session (type=session)
// or a directory to create a new session in (type=dir).
type Entry struct {
	Type    string // "session" or "dir"
	Name    string // session name or directory basename
	Path    string // the session's #{session_path}, or the full directory path
	Session string // session id ("$N") for a session entry, empty for dirs
	Agents  int    // agent panes in the session (0 for dirs and agent-less sessions)
}

var sessionFormat = tmux.Fields("#{session_id}", "#{session_path}", "#{session_name}")

// List merges open sessions and found directories, ordered for the picker:
// sessions first, then directories. Within each group, ordered by name.
// Every tmux session is listed, whether or not it has an agent.
func List(c *tmux.Client, roots []string, depth int) ([]Entry, error) {
	var entries []Entry

	// Open sessions first, annotated with their agent count.
	out, err := c.RunList("list-sessions", "-F", sessionFormat)
	if err != nil {
		return nil, err
	}
	agents := map[string]int{}
	if as, err := state.List(c); err == nil {
		for _, a := range as {
			agents[a.Session]++
		}
	}
	for _, line := range tmux.Lines(out) {
		// session_name goes last, so a tab in it can't shift the fields.
		f := strings.SplitN(line, tmux.FieldSep, 3)
		if len(f) != 3 {
			continue
		}
		entries = append(entries, Entry{
			Type:    "session",
			Name:    f[2],
			Path:    f[1],
			Session: f[0],
			Agents:  agents[f[2]],
		})
	}

	// Directories from roots.
	dirs, err := walkDirs(roots, depth)
	if err != nil {
		return entries, err
	}
	for _, dir := range dirs {
		entries = append(entries, Entry{
			Type: "dir",
			Name: filepath.Base(dir),
			Path: dir,
		})
	}

	// Sort within each group.
	slices.SortStableFunc(entries, func(a, b Entry) int {
		if a.Type != b.Type {
			if a.Type == "session" {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return entries, nil
}

// walkDirs walks roots to depth, collecting directories. Returns unique paths sorted.
func walkDirs(roots []string, depth int) ([]string, error) {
	dirs := make(map[string]bool)
	for _, root := range roots {
		_ = walk(root, 0, depth, dirs)
	}
	var out []string
	for d := range dirs {
		out = append(out, d)
	}
	slices.Sort(out)
	return out, nil
}

// walk recursively collects dirs to d <= maxDepth into the dirs map.
func walk(dir string, d, maxDepth int, dirs map[string]bool) error {
	if d > maxDepth {
		return nil
	}
	dirs[dir] = true
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil // skip unreadable dirs
	}
	for _, ent := range ents {
		if ent.IsDir() && !strings.HasPrefix(ent.Name(), ".") {
			path := filepath.Join(dir, ent.Name())
			walk(path, d+1, maxDepth, dirs)
		}
	}
	return nil
}

// SessionName extracts the name from a directory path, matching tmux-sessionizer's rule:
// strip leading digits, dots and hyphens, e.g. "12.34-herdmux" → "herdmux".
// Then "." and ":" become "_", as tmux does to a new session's name, so the
// name HasSession looks up is the one new-session made ("my.proj" → "my_proj").
func SessionName(dir string) string {
	if dir == "" {
		return ""
	}
	base := filepath.Base(dir)
	if base == "." || base == "/" || base == "" {
		return base
	}
	// Regex: ^[^A-Za-z]* matches leading non-letters; [A-Za-z]+ onwards is the name.
	re := regexp.MustCompile(`^[^A-Za-z]*(.*)`)
	matches := re.FindStringSubmatch(base)
	if len(matches) > 1 && matches[1] != "" {
		base = matches[1]
	}
	return tmuxName.Replace(base)
}

var tmuxName = strings.NewReplacer(".", "_", ":", "_")

// HasSession reports whether a session named exactly name exists. The "="
// prefix stops tmux matching a session whose name merely starts with name.
func HasSession(c *tmux.Client, name string) (bool, error) {
	_, err := c.Run("has-session", "-t", tmux.Literal("="+name))
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "can't find session") {
		return false, nil
	}
	return false, err
}

// NewSession creates a detached session with the given name and directory and
// returns its first pane id. new-session expands formats in -s and -c, so
// both are escaped: a dir named "x#(cmd)" must not run cmd.
func NewSession(c *tmux.Client, name, dir string) (string, error) {
	pane, err := c.Run("new-session", "-d", "-s", tmux.FormatLiteral(name), "-c", tmux.FormatLiteral(dir), "-P", "-F", "#{pane_id}")
	if err != nil {
		return "", fmt.Errorf("new-session %s: %w", name, err)
	}
	return strings.TrimSpace(pane), nil
}

// SwitchTo switches client to the entry. A session entry switches to its
// session id; a dir entry switches to the session named after the directory
// (SessionName), creating it there first if it doesn't exist.
func SwitchTo(c *tmux.Client, client string, entry Entry) error {
	if entry.Type == "session" {
		_, err := c.Run("switch-client", "-c", client, "-t", entry.Session)
		return err
	}

	name := SessionName(entry.Path)
	exists, err := HasSession(c, name)
	if err != nil {
		return err
	}
	if !exists {
		if _, err := NewSession(c, name, entry.Path); err != nil {
			return err
		}
	}
	_, err = c.Run("switch-client", "-c", client, "-t", tmux.Literal("="+name))
	return err
}
