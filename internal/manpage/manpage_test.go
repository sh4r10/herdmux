package manpage

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The page describes the tool only: no release metadata. .TH has an empty
// date and names only "herdmux", nothing is left of a version placeholder,
// and no YYYY-MM-DD date appears anywhere. (The version constant itself is
// checked in cmd/herdmux, TestManHasNoReleaseMetadata.)
func TestPageHasNoReleaseMetadata(t *testing.T) {
	t.Parallel()
	p := Page()
	th := regexp.MustCompile(`(?m)^\.TH .*$`).FindString(p)
	if th != `.TH HERDMUX 1 "" "herdmux" "User Commands"` {
		t.Errorf(".TH line %q", th)
	}
	if strings.Contains(p, "@VERSION@") {
		t.Error("page still contains @VERSION@")
	}
	if d := regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`).FindString(p); d != "" {
		t.Errorf("page contains the date %s", d)
	}
}

// TestPageLints runs the page through groff's man macros with every warning
// on; any diagnostic fails, except the one about .TH's empty date: the page
// carries no release metadata on purpose, and groff (1.23) raises that style
// note at every CHECKSTYLE level. Skips when groff is not installed.
func TestPageLints(t *testing.T) {
	t.Parallel()
	groff, err := exec.LookPath("groff")
	if err != nil {
		t.Skip("groff not installed")
	}
	cmd := exec.Command(groff, "-man", "-ww", "-z", "-rCHECKSTYLE=3")
	cmd.Stdin = strings.NewReader(Page())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	var diags []string
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if l != "" && !strings.Contains(l, ".TH missing third argument") {
			diags = append(diags, l)
		}
	}
	if err != nil || len(diags) > 0 {
		t.Fatalf("groff -man -ww -z: %v\n%s", err, strings.Join(diags, "\n"))
	}
}

// TestPageMandocLint runs mandoc's linter when it is installed.
func TestPageMandocLint(t *testing.T) {
	t.Parallel()
	mandoc, err := exec.LookPath("mandoc")
	if err != nil {
		t.Skip("mandoc not installed")
	}
	cmd := exec.Command(mandoc, "-T", "lint", "-W", "warning")
	cmd.Stdin = strings.NewReader(Page())
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("mandoc -T lint: %v\n%s", err, out)
	}
}
