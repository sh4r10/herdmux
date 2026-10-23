package main

import (
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sh4r10/herdmux/internal/tmuxtest"
)

func TestParallelRunsAllAndReturnsFirstErrorInOrder(t *testing.T) {
	t.Parallel()
	errA, errB := errors.New("a"), errors.New("b")
	var ran atomic.Int32
	err := parallel(
		func() error { ran.Add(1); return nil },
		func() error { time.Sleep(20 * time.Millisecond); ran.Add(1); return errA },
		func() error { ran.Add(1); return errB },
	)
	if err != errA {
		t.Errorf("err = %v, want %v (the first in argument order)", err, errA)
	}
	if ran.Load() != 3 {
		t.Errorf("%d of 3 funcs ran", ran.Load())
	}
	if err := parallel(func() error { return nil }); err != nil {
		t.Errorf("no errors: got %v", err)
	}
}

// A popup key starts herdmux twice (the opener, then the popup's inner
// run), so package init is on the key-to-first-frame path twice.
// go-runewidth v0.0.26+ fills a 2 MB table in init (~65 ms on WSL2); this
// fails if any package's init gets that slow again (go.mod pins runewidth
// below it).
func TestPackageInitIsCheap(t *testing.T) {
	t.Parallel()
	bin := tmuxtest.Binary(t)
	line := regexp.MustCompile(`(?m)^init (\S+) @\S+ ms, ([0-9.]+) ms clock`)
	worst := map[string]float64{} // best of three runs per package
	for range 3 {
		cmd := exec.Command(bin, "--version")
		cmd.Env = []string{"GODEBUG=inittrace=1", "HOME=" + t.TempDir()}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("herdmux --version: %v\n%s", err, out)
		}
		seen := map[string]float64{}
		for _, m := range line.FindAllStringSubmatch(string(out), -1) {
			ms, _ := strconv.ParseFloat(m[2], 64)
			seen[m[1]] = ms
		}
		if len(seen) == 0 {
			t.Fatalf("no inittrace lines in:\n%s", out)
		}
		for pkg, ms := range seen {
			if prev, ok := worst[pkg]; !ok || ms < prev {
				worst[pkg] = ms
			}
		}
	}
	for pkg, ms := range worst {
		if ms > 20 {
			t.Errorf("init of %s takes %.1f ms (best of 3); popups pay it twice per key", pkg, ms)
		}
	}
}
