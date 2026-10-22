package main

import (
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"herdmux/internal/manpage"
)

// defineMan is `herdmux man`: the embedded herdmux(1). On a terminal with
// man(1) on PATH the page goes through `man -l -` (man-db reads the page from
// stdin); otherwise the roff source is printed, so `herdmux man > herdmux.1`
// saves it.
func defineMan(fs *flag.FlagSet) func(*env, []string) error {
	return func(e *env, args []string) error {
		if len(args) > 0 {
			return usagef("man takes no arguments")
		}
		page := manpage.Page()
		if out, ok := e.stdout.(*os.File); ok && isTerminal(out) {
			if man := lookPath("man", e.getenv("PATH")); man != "" {
				cmd := exec.Command(man, "-l", "-")
				cmd.Stdin = strings.NewReader(page)
				cmd.Stdout, cmd.Stderr = e.stdout, e.stderr
				return cmd.Run()
			}
		}
		_, err := io.WriteString(e.stdout, page)
		return err
	}
}

// isTerminal reports whether f is a character device (a tty).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// lookPath finds an executable named prog in path (a PATH-style list), or "".
func lookPath(prog, path string) string {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, prog)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode().Perm()&0o111 != 0 {
			return p
		}
	}
	return ""
}
