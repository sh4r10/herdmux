package colorscheme

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/sh4r10/herdmux/internal/config"
)

//go:embed colors/*.toml
var bundledFS embed.FS

// BundledNames are the 25 bundled schemes in display order: flexoki first
// (the default), then each family in its official variant order.
var BundledNames = []string{
	"flexoki-dark", "flexoki-light",
	"tokyonight-night", "tokyonight-storm", "tokyonight-moon", "tokyonight-day",
	"catppuccin-mocha", "catppuccin-macchiato", "catppuccin-frappe", "catppuccin-latte",
	"kanagawa-wave", "kanagawa-dragon", "kanagawa-lotus",
	"rose-pine-main", "rose-pine-moon", "rose-pine-dawn",
	"gruvbox-dark", "gruvbox-light",
	"onedark-dark", "onedark-darker", "onedark-cool", "onedark-deep", "onedark-warm", "onedark-warmer", "onedark-light",
}

// Families in display order; user-only schemes are in "user", last.
var Families = []string{"flexoki", "tokyonight", "catppuccin", "kanagawa", "rose-pine", "gruvbox", "onedark", "user"}

// ErrNotFound is wrapped by every "no such scheme" error.
var ErrNotFound = errors.New("no such colorscheme")

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidName reports whether name can be a scheme (and so a file) name:
// letters, digits, '-' and '_', starting with a letter or digit.
func ValidName(name string) bool { return nameRE.MatchString(name) }

func familyOf(name string) string {
	for _, f := range Families {
		if strings.HasPrefix(name, f+"-") {
			return f
		}
	}
	return "user"
}

func bundledRaw(name string) ([]byte, bool) {
	if !slices.Contains(BundledNames, name) {
		return nil, false
	}
	b, err := bundledFS.ReadFile("colors/" + name + ".toml")
	return b, err == nil
}

// BundledScheme returns the embedded scheme, ignoring any user override.
func BundledScheme(name string) (Scheme, bool) {
	b, ok := bundledRaw(name)
	if !ok {
		return Scheme{}, false
	}
	s := Parse(name, "bundled:"+name+".toml", b)
	s.Family, s.Source = familyOf(name), Bundled
	return s, true
}

// DefaultScheme is the bundled flexoki-dark, always valid.
func DefaultScheme() Scheme {
	s, _ := BundledScheme(Default)
	return s
}

// Store is the schemes and saved choice under one config dir.
type Store struct {
	Dir string // the config dir (config.Dir)
}

// NewStore returns the store under the config dir dir.
func NewStore(dir string) *Store { return &Store{Dir: dir} }

// FromEnv returns the store under config.Dir(getenv).
func FromEnv(getenv func(string) string) (*Store, error) {
	d, err := config.Dir(getenv)
	if err != nil {
		return nil, err
	}
	return NewStore(d), nil
}

// ColorsDir holds the user schemes.
func (st *Store) ColorsDir() string { return filepath.Join(st.Dir, "colors") }

// UserPath is the user file for name.
func (st *Store) UserPath(name string) string { return filepath.Join(st.ColorsDir(), name+".toml") }

// SavedPath is the file holding the saved choice.
func (st *Store) SavedPath() string { return filepath.Join(st.Dir, "colorscheme") }

// userNames lists the user files' scheme names (files with an invalid name
// are skipped). A missing colors/ dir is no error.
func (st *Store) userNames() ([]string, error) {
	ents, err := os.ReadDir(st.ColorsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".toml")
		if ok && !e.IsDir() && ValidName(n) {
			names = append(names, n)
		}
	}
	return names, nil
}

func (st *Store) loadUser(name string) Scheme {
	path := st.UserPath(name)
	b, err := os.ReadFile(path)
	var s Scheme
	if err != nil {
		s = Scheme{Name: name, File: path, Colors: map[string]string{},
			Problems: []Problem{{Short: "unreadable", Long: err.Error()}}}
	} else {
		s = Parse(name, path, b)
	}
	s.Source, s.Family = User, "user"
	if slices.Contains(BundledNames, name) {
		s.Source, s.Family = Override, familyOf(name)
	}
	return s
}

// List returns every scheme, bundled and user together, a user file replacing
// the bundled scheme of the same name. Order: by family (Families), bundled
// order within a family, user-only schemes last by name. Saved marks the
// saved choice.
func (st *Store) List() ([]Scheme, error) {
	users, err := st.userNames()
	if err != nil {
		return nil, err
	}
	saved, err := st.Saved()
	if err != nil {
		return nil, err
	}
	var out []Scheme
	for _, n := range BundledNames {
		if slices.Contains(users, n) {
			out = append(out, st.loadUser(n))
		} else {
			s, _ := BundledScheme(n)
			out = append(out, s)
		}
	}
	sort.Strings(users)
	for _, n := range users {
		if !slices.Contains(BundledNames, n) {
			out = append(out, st.loadUser(n))
		}
	}
	for i := range out {
		out[i].Saved = out[i].Name == saved
	}
	return out, nil
}

// Get returns the effective scheme called name (the user file if there is
// one). An unknown name wraps ErrNotFound. The scheme may be invalid; check
// Valid.
func (st *Store) Get(name string) (Scheme, error) {
	if !ValidName(name) {
		return Scheme{}, fmt.Errorf("%w %q", ErrNotFound, name)
	}
	if _, err := os.Stat(st.UserPath(name)); err == nil {
		return st.loadUser(name), nil
	}
	if s, ok := BundledScheme(name); ok {
		return s, nil
	}
	return Scheme{}, fmt.Errorf("%w %q", ErrNotFound, name)
}

// Raw returns the effective scheme's TOML text.
func (st *Store) Raw(name string) ([]byte, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("%w %q", ErrNotFound, name)
	}
	b, err := os.ReadFile(st.UserPath(name))
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if b, ok := bundledRaw(name); ok {
		return b, nil
	}
	return nil, fmt.Errorf("%w %q", ErrNotFound, name)
}

// Saved returns the saved choice, Default when the file is missing or empty.
// The name is returned as written, even if no such scheme exists.
func (st *Store) Saved() (string, error) {
	b, err := os.ReadFile(st.SavedPath())
	if errors.Is(err, fs.ErrNotExist) {
		return Default, nil
	}
	if err != nil {
		return "", err
	}
	if n := strings.TrimSpace(string(b)); n != "" {
		return n, nil
	}
	return Default, nil
}

// Save stores name as the saved choice. It refuses an unknown or invalid
// scheme and then writes nothing. It doesn't apply it (see Set).
func (st *Store) Save(name string) error {
	s, err := st.Get(name)
	if err != nil {
		return err
	}
	if !s.Valid() {
		return s.Err()
	}
	return writeFile(st.SavedPath(), []byte(name+"\n"))
}

// Write validates text and saves it as the user scheme name (a bundled name
// creates an override). An invalid scheme is refused and nothing is written.
// It doesn't re-apply: if name is the active scheme, the caller applies it.
func (st *Store) Write(name string, text []byte) (Scheme, error) {
	if !ValidName(name) {
		return Scheme{}, fmt.Errorf("bad colorscheme name %q (letters, digits, - and _)", name)
	}
	path := st.UserPath(name)
	s := Parse(name, path, text)
	if !s.Valid() {
		return s, s.Err()
	}
	if err := writeFile(path, text); err != nil {
		return s, err
	}
	return st.loadUser(name), nil
}

// Delete removes the user scheme name. Bundled schemes can't be deleted;
// deleting an override brings the bundled scheme back. It doesn't re-apply:
// if name was active, the caller applies the effective scheme (ApplySaved).
func (st *Store) Delete(name string) error {
	if !ValidName(name) {
		return fmt.Errorf("%w %q", ErrNotFound, name)
	}
	err := os.Remove(st.UserPath(name))
	if errors.Is(err, fs.ErrNotExist) {
		if _, ok := bundledRaw(name); ok {
			return fmt.Errorf("%s is bundled; only user schemes can be deleted", name)
		}
		return fmt.Errorf("%w %q", ErrNotFound, name)
	}
	return err
}

// Resolved is the scheme that gets applied for the saved choice.
type Resolved struct {
	Scheme Scheme // valid; the fallback when Warning is set
	Saved  string // the saved name
	// Warning is set when the saved scheme is unknown or invalid and the
	// fallback is used. It names the file and the slot.
	Warning string
}

// Resolve gives the effective scheme for the saved choice. An unknown or
// invalid saved scheme falls back to flexoki-dark (the user's override of it
// if valid, else the bundled one), with a Warning; the saved file is left
// untouched. The error is only for an unreadable saved file or colors dir.
func (st *Store) Resolve() (Resolved, error) {
	saved, err := st.Saved()
	if err != nil {
		return Resolved{}, err
	}
	r := Resolved{Saved: saved}
	s, err := st.Get(saved)
	switch {
	case errors.Is(err, ErrNotFound):
		r.Warning = fmt.Sprintf("colorscheme %q (saved in %s) not found", saved, st.SavedPath())
	case err != nil:
		return Resolved{}, err
	case !s.Valid():
		r.Warning = "colorscheme " + s.Err().Error()
	default:
		r.Scheme = s
		return r, nil
	}
	r.Scheme = DefaultScheme()
	if saved != Default {
		if d, err := st.Get(Default); err == nil && d.Valid() {
			r.Scheme = d
		}
	}
	r.Warning += "; using " + Default
	return r, nil
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
