package colorscheme

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Default is the scheme used when nothing is saved, and the fallback for an
// unknown or invalid saved scheme.
const Default = "flexoki-dark"

// OptionalSlots maps each slot added after v1 to the v1 slot it defaults to,
// so older user files keep loading. v1 adds none. A later slot goes here, not
// in Slots.
var OptionalSlots = map[string]string{}

// Source is where a scheme comes from.
type Source string

const (
	Bundled  Source = "bundled"  // embedded in the binary
	User     Source = "user"     // a user file with a new name
	Override Source = "override" // a user file shadowing a bundled scheme
)

// Problem is one reason a scheme is invalid.
type Problem struct {
	Slot  string // the slot; "" for a problem with the whole file
	Value string // the bad value; "" when the slot is missing
	Short string // e.g. "bad border", "no attention_done", "bad TOML"
	Long  string // e.g. `border = "#12345" is not #rrggbb`
}

// Scheme is one loaded colorscheme.
type Scheme struct {
	Name   string
	Family string // flexoki, tokyonight, …; "user" for user-only schemes
	Light  bool   // pane_bg is light (relative luminance > 0.5)
	Source Source
	// File names the scheme's file: the user file's path, or
	// "bundled:<name>.toml".
	File string
	// Colors holds every valid slot as lowercase "#rrggbb" (in a tmux
	// format, "#D" and the like are aliases, so never store uppercase).
	Colors   map[string]string
	Problems []Problem
	// Ignored lists keys that aren't slots (in [colors] as "colors.<key>",
	// elsewhere by their dotted path). They are allowed and ignored, so a
	// file written for a later release, with more slots, still loads.
	Ignored []string
	// Saved marks the saved choice (set by Store.List).
	Saved bool
}

// Valid reports whether the scheme can be applied.
func (s Scheme) Valid() bool { return len(s.Problems) == 0 }

// Reason is the short reason a scheme is invalid, for the picker and
// `--list`: "bad border, no attention_done". Empty when valid.
func (s Scheme) Reason() string {
	var r []string
	for _, p := range s.Problems {
		r = append(r, p.Short)
	}
	return strings.Join(r, ", ")
}

// Err names the file and every bad slot, or is nil when the scheme is valid.
func (s Scheme) Err() error {
	if s.Valid() {
		return nil
	}
	var r []string
	for _, p := range s.Problems {
		r = append(r, p.Long)
	}
	return fmt.Errorf("%s: %s", s.File, strings.Join(r, "; "))
}

// Tone is "light" or "dark".
func (s Scheme) Tone() string {
	if s.Light {
		return "light"
	}
	return "dark"
}

var hexRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Parse validates one scheme's TOML. name and file go into the result (file
// is what error messages name); Family and Source are left for the caller.
//
// Rules: every slot of Slots must be a "#rrggbb" string in [colors] (any case;
// stored lowercase). Slots in OptionalSlots default to their v1 slot. Other
// keys and tables are ignored (listed in Ignored).
func Parse(name, file string, data []byte) Scheme {
	s := Scheme{Name: name, File: file, Colors: map[string]string{}}
	var doc map[string]any
	md, err := toml.Decode(string(data), &doc)
	if err != nil {
		long := err.Error()
		var pe toml.ParseError
		if errors.As(err, &pe) {
			long = fmt.Sprintf("bad TOML at line %d: %s", pe.Position.Line, pe.Message)
		}
		s.Problems = []Problem{{Short: "bad TOML", Long: long}}
		return s
	}
	colors, ok := doc["colors"].(map[string]any)
	if !ok {
		s.Problems = []Problem{{Short: "no [colors]", Long: "no [colors] table"}}
		return s
	}
	for _, k := range md.Keys() {
		key := k.String()
		if len(k) == 0 || key == "colors" {
			continue
		}
		if len(k) == 2 && k[0] == "colors" && (slices.Contains(Slots, k[1]) || OptionalSlots[k[1]] != "") {
			continue
		}
		if len(k) > 2 && k[0] == "colors" {
			continue // inside an ignored [colors.x] table: its parent is listed
		}
		if len(k) > 1 && k[0] != "colors" {
			continue // inside an ignored top-level table
		}
		s.Ignored = append(s.Ignored, key)
	}
	check := func(slot string, v any) {
		str, isStr := v.(string)
		switch {
		case !isStr:
			val := fmt.Sprint(v)
			s.Problems = append(s.Problems, Problem{Slot: slot, Value: val, Short: "bad " + slot,
				Long: fmt.Sprintf("%s = %s is not a \"#rrggbb\" string", slot, val)})
		case !hexRE.MatchString(str):
			s.Problems = append(s.Problems, Problem{Slot: slot, Value: str, Short: "bad " + slot,
				Long: fmt.Sprintf("%s = %q is not #rrggbb", slot, str)})
		default:
			s.Colors[slot] = strings.ToLower(str)
		}
	}
	for _, slot := range Slots {
		v, ok := colors[slot]
		if !ok {
			s.Problems = append(s.Problems, Problem{Slot: slot, Short: "no " + slot, Long: slot + " missing"})
			continue
		}
		check(slot, v)
	}
	for slot, def := range OptionalSlots {
		if v, ok := colors[slot]; ok {
			check(slot, v)
		} else if c, ok := s.Colors[def]; ok {
			s.Colors[slot] = c
		}
	}
	if bg, ok := s.Colors["pane_bg"]; ok {
		s.Light = luminance(bg) > 0.5
	}
	return s
}

// luminance is the Rec. 709 weighted lightness of "#rrggbb", 0..1.
func luminance(hex string) float64 {
	r, _ := strconv.ParseUint(hex[1:3], 16, 8)
	g, _ := strconv.ParseUint(hex[3:5], 16, 8)
	b, _ := strconv.ParseUint(hex[5:7], 16, 8)
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255
}
