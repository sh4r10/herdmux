// Package config parses $HERDMUX_CONFIG_DIR/config.toml: the [[agent]] launch
// entries and the [keys] bindings, with their defaults and fallbacks. Bind
// applies [keys] to a tmux server.
//
// Parse never leaves the caller without a usable Config: a broken file falls
// back to the defaults of the broken part and returns an *Error naming every
// problem ("config.toml: …"), which callers show in red.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// FileName is the config file's name inside Dir.
const FileName = "config.toml"

// Agent is one [[agent]] entry: a launch-menu item.
type Agent struct {
	Name    string // the window name; also `herdmux launch <name>`
	Key     string // one character; auto-picked from Name when the file omits it
	Command string // typed into the new window's shell verbatim
}

// Actions are the [keys] action names, in help order. They are the herdmux
// verbs the bindings run.
var Actions = []string{"picker", "next-attention", "organiser", "launch", "sessionizer", "colorscheme"}

// DefaultAgents is the launch menu with no file, or no [[agent]] entries.
func DefaultAgents() []Agent {
	return []Agent{
		{Name: "claude", Key: "c", Command: "claude"},
		{Name: "opencode", Key: "o", Command: "opencode"},
		{Name: "pi", Key: "p", Command: "pi"},
	}
}

// DefaultKeys maps every action to its default key spec. Everything sits
// behind the prefix; colorscheme is unbound.
func DefaultKeys() map[string]string {
	return map[string]string{
		"picker":         "prefix a",
		"next-attention": "prefix u",
		"organiser":      "prefix r",
		"launch":         "prefix o",
		"sessionizer":    "prefix g",
		"colorscheme":    "",
	}
}

// Sessionizer is the [sessionizer] section: roots to search and depth limit.
type Sessionizer struct {
	Roots []string // directories to walk (e.g. ["/home/user/projects"])
	Depth int      // maximum directory depth (0 = roots only)
}

// DefaultSessionizer is the sessionizer with no config file.
func DefaultSessionizer() Sessionizer {
	return Sessionizer{Roots: []string{}, Depth: 3}
}

// Config is a parsed config.toml.
type Config struct {
	Agents      []Agent           // menu order, every Key set
	Keys        map[string]string // every action in Actions → its spec ("" = unbound)
	Sessionizer Sessionizer       // [sessionizer] roots and depth
}

// Defaults is the config with no file.
func Defaults() Config {
	return Config{
		Agents:      DefaultAgents(),
		Keys:        DefaultKeys(),
		Sessionizer: DefaultSessionizer(),
	}
}

// Error is a broken config.toml: every problem found, in file order.
type Error struct{ Problems []string }

func (e *Error) Error() string { return FileName + ": " + strings.Join(e.Problems, "; ") }

// Path is $HERDMUX_CONFIG_DIR/config.toml (default ~/.config/herdmux/config.toml).
func Path(getenv func(string) string) (string, error) {
	dir, err := Dir(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads and parses config.toml. A missing file is the defaults with no
// error. It always returns a usable Config, even with an error.
func Load(getenv func(string) string) (Config, error) {
	p, err := Path(getenv)
	if err != nil {
		return Defaults(), err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), &Error{Problems: []string{err.Error()}}
	}
	return Parse(data)
}

// Write validates text with Parse and, only if it has no problems, saves it
// as config.toml atomically. It is MCP write_config's save.
func Write(getenv func(string) string, text []byte) (Config, error) {
	cfg, err := Parse(text)
	if err != nil {
		return cfg, err
	}
	p, err := Path(getenv)
	if err != nil {
		return cfg, err
	}
	return cfg, writeFile(p, text)
}

// The raw file shape. Pointers tell a missing field from an empty one.
type rawAgent struct {
	Name    *string `toml:"name"`
	Key     *string `toml:"key"`
	Command *string `toml:"command"`
}

type rawSessionizer struct {
	Roots []string `toml:"roots"`
	Depth *int     `toml:"depth"`
}

type rawFile struct {
	Agent       []rawAgent        `toml:"agent"`
	Keys        map[string]string `toml:"keys"`
	Sessionizer rawSessionizer    `toml:"sessionizer"`
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// Parse parses config.toml text. Fallbacks, each with its problem in the
// returned *Error:
//
//   - bad TOML (syntax, wrong types): everything is the defaults;
//   - an [[agent]] entry with a missing or bad name, command or key: the
//     agents are the defaults;
//   - a duplicate agent key or name: the first entry wins, the later one is
//     dropped;
//   - a bad [keys] entry (unknown action, bad key, two actions on one key):
//     the keys are the defaults;
//   - an unknown key anywhere else: reported only.
func Parse(data []byte) (Config, error) {
	var raw rawFile
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		var pe toml.ParseError
		if errors.As(err, &pe) {
			return Defaults(), &Error{Problems: []string{fmt.Sprintf("line %d: %s", pe.Position.Line, pe.Message)}}
		}
		return Defaults(), &Error{Problems: []string{strings.TrimPrefix(err.Error(), "toml: ")}}
	}
	var problems []string
	for _, k := range md.Undecoded() {
		problems = append(problems, fmt.Sprintf("unknown key %q", k.String()))
	}
	cfg := Defaults()
	agents, ap := parseAgents(raw.Agent)
	problems = append(problems, ap...)
	if agents != nil {
		cfg.Agents = agents
	}
	keys, kp := parseKeys(raw.Keys)
	problems = append(problems, kp...)
	if kp == nil {
		cfg.Keys = keys
	}
	sessionizer, sp := parseSessionizer(raw.Sessionizer)
	problems = append(problems, sp...)
	if sessionizer != nil {
		cfg.Sessionizer = *sessionizer
	}
	if len(problems) > 0 {
		return cfg, &Error{Problems: problems}
	}
	return cfg, nil
}

// parseAgents returns nil agents (use the defaults) when there are none or
// an entry is broken.
func parseAgents(raw []rawAgent) ([]Agent, []string) {
	if len(raw) == 0 {
		return nil, nil
	}
	var problems []string
	broken := false
	label := func(i int, r rawAgent) string {
		if r.Name != nil && *r.Name != "" {
			return fmt.Sprintf("agent %q", *r.Name)
		}
		return fmt.Sprintf("agent #%d", i+1)
	}
	for i, r := range raw {
		switch {
		case r.Name == nil || *r.Name == "":
			problems = append(problems, label(i, r)+": missing name")
			broken = true
		case !nameRE.MatchString(*r.Name):
			problems = append(problems, fmt.Sprintf("%s: bad name (want letters, digits, '.', '_' or '-')", label(i, r)))
			broken = true
		}
		if r.Command == nil || strings.TrimSpace(*r.Command) == "" {
			problems = append(problems, label(i, r)+": missing command")
			broken = true
		} else if strings.ContainsAny(*r.Command, "\n\r") {
			problems = append(problems, label(i, r)+": command must be one line")
			broken = true
		}
		if r.Key != nil {
			if p := keyProblem(*r.Key); p != "" {
				problems = append(problems, fmt.Sprintf("%s: key %q %s", label(i, r), *r.Key, p))
				broken = true
			}
		}
	}
	if broken {
		return nil, append(problems, "using the default agents")
	}

	// Explicit keys and names first: the first entry wins, a later duplicate
	// is dropped.
	usedKey, usedName := map[string]bool{"q": true}, map[string]bool{}
	var kept []rawAgent
	for _, r := range raw {
		if usedName[*r.Name] {
			problems = append(problems, fmt.Sprintf("duplicate agent name %q (the first one wins)", *r.Name))
			continue
		}
		if r.Key != nil && usedKey[*r.Key] {
			problems = append(problems, fmt.Sprintf("duplicate key %q on agent %q (the first one wins)", *r.Key, *r.Name))
			continue
		}
		usedName[*r.Name] = true
		if r.Key != nil {
			usedKey[*r.Key] = true
		}
		kept = append(kept, r)
	}
	// Then the auto-pick: the first letter or digit of the name not used yet.
	var out []Agent
	for _, r := range kept {
		a := Agent{Name: *r.Name, Command: strings.TrimSpace(*r.Command)}
		if r.Key != nil {
			a.Key = *r.Key
		} else {
			for _, ch := range strings.ToLower(a.Name) {
				k := string(ch)
				if (unicode.IsLetter(ch) || unicode.IsDigit(ch)) && !usedKey[k] {
					a.Key = k
					break
				}
			}
			if a.Key == "" {
				problems = append(problems, fmt.Sprintf("agent %q: no free key in its name; set key", a.Name))
				continue
			}
			usedKey[a.Key] = true
		}
		out = append(out, a)
	}
	return out, problems
}

// keyProblem checks an [[agent]] key: one printable character, not q (q
// closes the prompt) and not a space.
func keyProblem(k string) string {
	switch {
	case utf8.RuneCountInString(k) != 1:
		return "must be one character"
	case k == "q":
		return "is reserved (q closes the prompt)"
	}
	r, _ := utf8.DecodeRuneInString(k)
	if !unicode.IsPrint(r) || unicode.IsSpace(r) {
		return "must be a printable character"
	}
	return ""
}

// parseKeys merges [keys] over the defaults. Any problem means the defaults
// (problems is non-nil then).
func parseKeys(raw map[string]string) (map[string]string, []string) {
	keys := DefaultKeys()
	var problems []string
	for _, action := range sortedKeys(raw) {
		spec := raw[action]
		if _, ok := keys[action]; !ok {
			problems = append(problems, fmt.Sprintf("[keys] unknown action %q (want %s)", action, strings.Join(Actions, ", ")))
			continue
		}
		norm, err := ParseSpec(spec)
		if err != nil {
			problems = append(problems, fmt.Sprintf("[keys] %s = %q: %v", action, spec, err))
			continue
		}
		keys[action] = norm.String()
	}
	seen := map[string]string{}
	for _, action := range Actions {
		spec := keys[action]
		if spec == "" {
			continue
		}
		if other, ok := seen[spec]; ok {
			problems = append(problems, fmt.Sprintf("[keys] %s and %s are both %q", other, action, spec))
		}
		seen[spec] = action
	}
	if problems != nil {
		return DefaultKeys(), append(problems, "using the default keys")
	}
	return keys, nil
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for _, a := range Actions { // known actions in Actions order, then the rest sorted
		if _, ok := m[a]; ok {
			ks = append(ks, a)
		}
	}
	var rest []string
	for k := range m {
		if _, ok := DefaultKeys()[k]; !ok {
			rest = append(rest, k)
		}
	}
	slices.Sort(rest)
	return append(ks, rest...)
}

// parseSessionizer parses the [sessionizer] section. Returns nil (use
// defaults) when there are no roots. Bad depth is silently ignored (uses default).
func parseSessionizer(raw rawSessionizer) (*Sessionizer, []string) {
	if len(raw.Roots) == 0 {
		return nil, nil
	}
	depth := DefaultSessionizer().Depth
	if raw.Depth != nil && *raw.Depth >= 0 {
		depth = *raw.Depth
	}
	return &Sessionizer{Roots: raw.Roots, Depth: depth}, nil
}

// Agent returns the entry named name.
func (c Config) Agent(name string) (Agent, bool) {
	for _, a := range c.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// Visible is the launch menu as shown: the agents whose command's first word
// (after any leading VAR=value words) is on path, a PATH-style list. Others
// are hidden silently, so one config works on any machine.
func (c Config) Visible(path string) []Agent {
	var out []Agent
	for _, a := range c.Agents {
		if onPath(program(a.Command), path) {
			out = append(out, a)
		}
	}
	return out
}

// program is a command's first word, skipping VAR=value assignments.
func program(command string) string {
	for _, w := range strings.Fields(command) {
		if i := strings.IndexByte(w, '='); i > 0 && !strings.ContainsRune(w[:i], '/') {
			continue
		}
		return w
	}
	return ""
}

func onPath(prog, path string) bool {
	if prog == "" {
		return false
	}
	if strings.ContainsRune(prog, '/') {
		return executable(prog)
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		if executable(filepath.Join(dir, prog)) {
			return true
		}
	}
	return false
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0
}

// writeFile writes atomically (temp file + rename), creating the dir.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}
