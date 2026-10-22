package main

import (
	"regexp"
	"strings"
	"testing"

	"herdmux/internal/config"
)

// Not a terminal (a buffer): `herdmux man` prints the roff source as is.
// The page describes the tool, not a release: neither the version constant
// nor a YYYY-MM-DD date may appear in it.
func TestManHasNoReleaseMetadata(t *testing.T) {
	t.Parallel()
	r := runCLI(t, map[string]string{"PATH": "/usr/bin:/bin"}, "man")
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	if strings.Contains(r.stdout, version) {
		t.Errorf("the page contains the version %s", version)
	}
	if d := regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`).FindString(r.stdout); d != "" {
		t.Errorf("the page contains the date %s", d)
	}
}

func TestManPrintsRoffWhenNotATerminal(t *testing.T) {
	t.Parallel()
	r := runCLI(t, map[string]string{"PATH": "/usr/bin:/bin"}, "man")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	if !strings.HasPrefix(r.stdout, `.\"`) || !strings.Contains(r.stdout, "\n.TH HERDMUX 1 ") {
		t.Fatalf("not roff: %q", r.stdout[:min(200, len(r.stdout))])
	}
	if !strings.Contains(r.stdout, "\n.TH HERDMUX 1 \"\" \"herdmux\" \"User Commands\"\n") || strings.Contains(r.stdout, "@VERSION@") {
		t.Errorf(".TH should carry no date or version")
	}
	for _, sec := range []string{"NAME", "SYNOPSIS", "DESCRIPTION", "COMMANDS", "KEY BINDINGS",
		"CONFIGURATION", "SESSIONIZER", "COLORSCHEMES", "AGENT STATES AND ATTENTION",
		"ENVIRONMENT", "FILES", "SEE ALSO"} {
		if !strings.Contains(r.stdout, "\n.SH "+sec+"\n") {
			t.Errorf("missing section %s", sec)
		}
	}
	if r := runCLI(t, nil, "man", "extra"); r.code != 2 {
		t.Errorf("man extra: code=%d, want 2", r.code)
	}
}

// Every verb in the dispatch table, hidden ones included, has an entry in
// the page's COMMANDS section, so the manual can't drift from the CLI.
func TestManDocumentsEveryVerb(t *testing.T) {
	t.Parallel()
	page := runCLI(t, nil, "man").stdout
	_, cmds, _ := strings.Cut(page, "\n.SH COMMANDS\n")
	cmds, _, _ = strings.Cut(cmds, "\n.SH ")
	// An entry is a .TP tag line: ".B herdmux <verb>…", with "-" escaped.
	tags := map[string]bool{}
	re := regexp.MustCompile(`(?m)^\.TP\n\.B herdmux ((?:\\-|[a-z])+)`)
	for _, m := range re.FindAllStringSubmatch(cmds, -1) {
		tags[strings.ReplaceAll(m[1], `\-`, "-")] = true
	}
	for _, v := range verbs {
		if !tags[v.name] {
			t.Errorf("COMMANDS has no entry for %q", v.name)
		}
	}
	// Every [keys] action appears in the page's default [keys] block.
	for _, a := range config.Actions {
		if !strings.Contains(page, "\n"+strings.ReplaceAll(a, "-", `\-`)+" = ") {
			t.Errorf("the [keys] defaults lack %s", a)
		}
	}
}
