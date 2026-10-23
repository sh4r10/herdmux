package colorscheme

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

// valid is a complete scheme body; tests edit it.
func valid() map[string]string {
	s, _ := BundledScheme("gruvbox-dark")
	return s.Colors
}

func schemeTOML(colors map[string]string, extra string) []byte {
	var b strings.Builder
	b.WriteString("# source: test\n" + extra + "\n[colors]\n")
	for _, slot := range Slots {
		if v, ok := colors[slot]; ok {
			b.WriteString(slot + ` = "` + v + "\"\n")
		}
	}
	return []byte(b.String())
}

func TestBundledSchemes(t *testing.T) {
	t.Parallel()
	ents, err := bundledFS.ReadDir("colors")
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range ents {
		files = append(files, strings.TrimSuffix(e.Name(), ".toml"))
	}
	want := slices.Clone(BundledNames)
	slices.Sort(want)
	if len(BundledNames) != 25 || !slices.Equal(files, want) {
		t.Fatalf("embedded %v, want the 25 %v", files, want)
	}
	lights := []string{"flexoki-light", "tokyonight-day", "catppuccin-latte", "kanagawa-lotus", "rose-pine-dawn", "gruvbox-light", "onedark-light"}
	sourceRE := regexp.MustCompile(`(?m)^# source: https://\S+`)
	for _, n := range BundledNames {
		s, ok := BundledScheme(n)
		if !ok || !s.Valid() || len(s.Ignored) > 0 {
			t.Errorf("%s: ok=%v err=%v ignored=%v", n, ok, s.Err(), s.Ignored)
			continue
		}
		raw, _ := bundledRaw(n)
		if !sourceRE.Match(raw) {
			t.Errorf("%s cites no source", n)
		}
		if regexp.MustCompile(`"#[0-9a-f]*[A-F]`).Match(raw) {
			t.Errorf("%s has uppercase hex", n)
		}
		if s.Light != slices.Contains(lights, n) {
			t.Errorf("%s: light=%v", n, s.Light)
		}
		if s.Family == "user" || s.Source != Bundled {
			t.Errorf("%s: family %q source %q", n, s.Family, s.Source)
		}
	}
	if got := DefaultScheme().Colors["state_working"]; got != "#d0a215" {
		t.Errorf("flexoki-dark state_working = %s, want #d0a215 (#D0A215)", got)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	missing := valid()
	delete(missing, "attention_done")
	bad := valid()
	bad["border"] = "#12345"
	bad["accent"] = "red"
	s := Parse("x", "/c/colors/x.toml", schemeTOML(missing, ""))
	if s.Valid() || s.Reason() != "no attention_done" || !strings.Contains(s.Err().Error(), "/c/colors/x.toml: attention_done missing") {
		t.Errorf("missing: reason %q err %v", s.Reason(), s.Err())
	}
	s = Parse("x", "x.toml", schemeTOML(bad, ""))
	if s.Reason() != "bad border, bad accent" {
		t.Errorf("bad hex: reason %q", s.Reason())
	}
	if e := s.Err().Error(); !strings.Contains(e, `border = "#12345" is not #rrggbb`) || !strings.Contains(e, `accent = "red"`) {
		t.Errorf("bad hex: %s", e)
	}
	both := valid()
	both["border"] = "#12345"
	delete(both, "attention_done")
	if r := Parse("x", "x", schemeTOML(both, "")).Reason(); r != "bad border, no attention_done" {
		t.Errorf("picker reason %q", r)
	}

	// Extra keys (top-level, in [colors] and other tables) are ignored.
	extra := schemeTOML(valid(), "name = \"x\"\n[meta]\nauthor = \"me\"")
	extra = append(extra, []byte("future_slot = \"#000000\"\n[colors.sub]\nk = 1\n")...)
	s = Parse("x", "x", extra)
	if !s.Valid() || !slices.Equal(s.Ignored, []string{"name", "meta", "colors.future_slot", "colors.sub"}) {
		t.Errorf("extra keys: err=%v ignored=%v", s.Err(), s.Ignored)
	}

	// Uppercase is accepted and stored lowercase.
	up := valid()
	up["bg"] = "#ABCDEF"
	if s := Parse("x", "x", schemeTOML(up, "")); s.Colors["bg"] != "#abcdef" {
		t.Errorf("bg stored %q", s.Colors["bg"])
	}

	for _, tc := range []struct{ text, reason string }{
		{"[colors\n", "bad TOML"},
		{"bg = \"#000000\"\n", "no [colors]"},
		{"[colors]\npane_bg = 5\n", "bad pane_bg"},
	} {
		if r := Parse("x", "x", []byte(tc.text)).Reason(); !strings.HasPrefix(r, tc.reason) {
			t.Errorf("%q: reason %q, want %q…", tc.text, r, tc.reason)
		}
	}
}

func TestOptionalSlotsDefault(t *testing.T) {
	// Not parallel: it changes the package's OptionalSlots.
	OptionalSlots["tab_separator"] = "border"
	defer delete(OptionalSlots, "tab_separator")
	s := Parse("x", "x", schemeTOML(valid(), ""))
	if !s.Valid() || s.Colors["tab_separator"] != s.Colors["border"] {
		t.Fatalf("optional slot: err=%v got %q", s.Err(), s.Colors["tab_separator"])
	}
}

func writeUser(t *testing.T, st *Store, name string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(st.ColorsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.UserPath(name), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(t *testing.T, all []Scheme, name string) Scheme {
	t.Helper()
	for _, s := range all {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("%s not listed", name)
	return Scheme{}
}

func TestStoreShadowAndDelete(t *testing.T) {
	t.Parallel()
	st := NewStore(t.TempDir())
	pink := valid()
	pink["accent"] = "#ff00ff"
	writeUser(t, st, "tokyonight-night", schemeTOML(pink, ""))
	writeUser(t, st, "midnight-moss", schemeTOML(valid(), ""))
	broken := valid()
	broken["border"] = "#12345"
	writeUser(t, st, "broken-neon", schemeTOML(broken, ""))
	writeUser(t, st, "bad.name", schemeTOML(valid(), "")) // not a valid name: skipped

	all, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 27 {
		t.Fatalf("listed %d schemes, want 25 + 2 user", len(all))
	}
	if s := find(t, all, "tokyonight-night"); s.Source != Override || s.Family != "tokyonight" || s.Colors["accent"] != "#ff00ff" {
		t.Errorf("override: %+v", s)
	}
	if s := find(t, all, "midnight-moss"); s.Source != User || s.Family != "user" {
		t.Errorf("user: %+v", s)
	}
	if s := find(t, all, "broken-neon"); s.Valid() || s.Reason() != "bad border" {
		t.Errorf("broken: %q", s.Reason())
	}
	if all[0].Name != Default || !all[0].Saved || all[len(all)-1].Name != "midnight-moss" {
		t.Errorf("order/saved: first %s (saved %v), last %s", all[0].Name, all[0].Saved, all[len(all)-1].Name)
	}

	if s, _ := st.Get("tokyonight-night"); s.Colors["accent"] != "#ff00ff" {
		t.Errorf("Get prefers the user file: %s", s.Colors["accent"])
	}
	if raw, _ := st.Raw("tokyonight-night"); !strings.Contains(string(raw), "#ff00ff") {
		t.Errorf("Raw prefers the user file")
	}
	if err := st.Delete("tokyonight-night"); err != nil {
		t.Fatal(err)
	}
	if s, _ := st.Get("tokyonight-night"); s.Source != Bundled || s.Colors["accent"] != "#7aa2f7" {
		t.Errorf("after delete: %+v", s)
	}
	if err := st.Delete("tokyonight-night"); err == nil || !strings.Contains(err.Error(), "bundled") {
		t.Errorf("deleting a bundled scheme: %v", err)
	}
	if err := st.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting an unknown scheme: %v", err)
	}
	if _, err := st.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get unknown: %v", err)
	}
}

func TestStoreWriteAndSave(t *testing.T) {
	t.Parallel()
	st := NewStore(t.TempDir())
	if n, _ := st.Saved(); n != Default {
		t.Errorf("default saved %q", n)
	}
	broken := valid()
	delete(broken, "fg")
	if _, err := st.Write("neon", schemeTOML(broken, "")); err == nil {
		t.Error("Write accepted an invalid scheme")
	}
	if _, err := os.Stat(st.UserPath("neon")); err == nil {
		t.Error("an invalid scheme was written")
	}
	if _, err := st.Write("../evil", schemeTOML(valid(), "")); err == nil {
		t.Error("Write accepted a path")
	}
	s, err := st.Write("gruvbox-dark", schemeTOML(valid(), ""))
	if err != nil || s.Source != Override {
		t.Fatalf("Write shadow: %v %+v", err, s)
	}
	if err := st.Save("nope"); err == nil {
		t.Error("Save accepted an unknown scheme")
	}
	writeUser(t, st, "neon", schemeTOML(broken, ""))
	if err := st.Save("neon"); err == nil {
		t.Error("Save accepted an invalid scheme")
	}
	if _, err := os.Stat(st.SavedPath()); err == nil {
		t.Error("a refused Save wrote the file")
	}
	if err := st.Save("gruvbox-dark"); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.Saved(); n != "gruvbox-dark" {
		t.Errorf("saved %q", n)
	}
}

func TestFromEnvHonoursConfigDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := FromEnv(func(k string) string {
		return map[string]string{"HERDMUX_CONFIG_DIR": dir, "HOME": "/nonexistent"}[k]
	})
	if err != nil || st.ColorsDir() != filepath.Join(dir, "colors") || st.SavedPath() != filepath.Join(dir, "colorscheme") {
		t.Fatalf("%v %+v", err, st)
	}
	st, _ = FromEnv(func(k string) string { return map[string]string{"HOME": "/h"}[k] })
	if st.Dir != "/h/.config/herdmux" {
		t.Errorf("default dir %q", st.Dir)
	}
}

func TestResolveFallback(t *testing.T) {
	t.Parallel()
	st := NewStore(t.TempDir())
	if err := os.WriteFile(st.SavedPath(), []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := st.Resolve()
	if err != nil || r.Scheme.Name != Default || r.Saved != "nope" || !strings.Contains(r.Warning, `"nope"`) {
		t.Errorf("unknown: %v %+v", err, r)
	}
	broken := valid()
	delete(broken, "attention_done")
	writeUser(t, st, "neon", schemeTOML(broken, ""))
	if err := os.WriteFile(st.SavedPath(), []byte("neon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ = st.Resolve()
	if r.Scheme.Name != Default || !strings.Contains(r.Warning, st.UserPath("neon")) || !strings.Contains(r.Warning, "attention_done") {
		t.Errorf("invalid: %+v", r)
	}
}

func TestApplySetsEveryOption(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	g, _ := BundledScheme("gruvbox-dark")
	if err := Apply(s.Client, g); err != nil {
		t.Fatal(err)
	}
	for _, slot := range Slots {
		if got := s.Tmux("show-options", "-gv", Option(slot)); got != g.Colors[slot] {
			t.Errorf("%s = %q, want %q", slot, got, g.Colors[slot])
		}
	}
	if got, _ := Active(s.Client); got != "gruvbox-dark" {
		t.Errorf("active %q", got)
	}
	want := map[string]string{
		"window-active-style":         "bg=#282828,fg=#ebdbb2",
		"window-style":                "bg=#282828,fg=#ebdbb2",
		"window-status-current-style": "bg=#504945,fg=#ebdbb2,bold",
		"pane-active-border-style":    "fg=#83a598",
		"menu-style":                  "bg=#3c3836,fg=#ebdbb2",
		"mode-style":                  "bg=#fabd2f,fg=#282828",
		"clock-mode-colour":           "#fabd2f",
	}
	for _, kv := range StyleOptions(g) {
		got := s.Tmux("show-options", "-gv", kv[0])
		if got != kv[1] {
			t.Errorf("%s = %q, want %q", kv[0], got, kv[1])
		}
		if w, ok := want[kv[0]]; ok && got != w {
			t.Errorf("%s = %q, want %q", kv[0], got, w)
		}
	}
	if err := Apply(s.Client, Parse("x", "x", []byte("[colors]\n"))); err == nil {
		t.Error("Apply accepted an invalid scheme")
	}
}

func TestApplySavedAndSet(t *testing.T) {
	t.Parallel()
	s := tmuxtest.New(t)
	st := NewStore(s.ConfigDir)
	if err := os.MkdirAll(st.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.SavedPath(), []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := ApplySaved(s.Client, st)
	if err != nil || r.Warning == "" {
		t.Fatalf("%v %+v", err, r)
	}
	if got, _ := Active(s.Client); got != Default {
		t.Errorf("fallback active %q", got)
	}
	if b, _ := os.ReadFile(st.SavedPath()); string(b) != "nope\n" {
		t.Errorf("saved file changed: %q", b)
	}
	if _, err := Set(s.Client, st, "nope"); err == nil {
		t.Error("Set accepted an unknown scheme")
	}
	if _, err := Set(s.Client, st, "catppuccin-latte"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Active(s.Client); got != "catppuccin-latte" {
		t.Errorf("active %q", got)
	}
	if n, _ := st.Saved(); n != "catppuccin-latte" {
		t.Errorf("saved %q", n)
	}
}
