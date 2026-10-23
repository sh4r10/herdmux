package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/sh4r10/herdmux/internal/logfile"
)

// version is herdmux's release.
const version = "1.4.0"

// env is everything a verb may touch of the outside world. Verbs read the
// environment only through getenv, so tests can run them in-process and in
// parallel.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	getenv         func(string) string
	ppid           int // the parent's pid (report's default --pid); 0 if unknown
}

// verb is one flat CLI command.
type verb struct {
	name     string // as typed; also the [keys] action name
	synopsis string // positional arguments, e.g. "[agent]"
	summary  string // one line for help
	hidden   bool   // shown only by `help --all`
	// needsTmux makes the verb exit 2 with "herdmux: not inside tmux" when
	// $TMUX is unset (or, for neverFail verbs, log that and exit 0).
	needsTmux bool
	// neverFail verbs (hook, report, tmux-hook) always exit 0: errors, bad
	// usage and panics are written to the log instead.
	neverFail bool
	// define registers the verb's flags on fs and returns the function that
	// runs it once the flags are parsed. args are the positional arguments.
	define func(fs *flag.FlagSet) func(e *env, args []string) error
}

// Errors a verb's run function can return to pick its exit code.
var errNotInTmux = errors.New("not inside tmux")

// shownError is an error already shown to the user on their tmux client (a
// verb run from a key binding or a popup): exit 1 with nothing on stderr.
type shownError struct{ error }

func (s shownError) Unwrap() error { return s.error }

// usageError is bad arguments: exit 2 with the verb's usage on stderr.
type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

// run is the whole CLI: it returns the process exit code.
//
// Exit codes: 0 success, 1 failure, 2 usage
// errors and "not inside tmux". neverFail verbs always return 0.
func run(args []string, e *env) int {
	if len(args) == 0 {
		printHelp(e.stdout, false)
		return 0
	}
	switch args[0] {
	case "--version", "-version", "-v":
		fmt.Fprintf(e.stdout, "herdmux %s\n", version)
		return 0
	case "--help", "-help", "-h":
		args = []string{"help"}
	}
	v := lookup(args[0])
	if v == nil {
		fmt.Fprintf(e.stderr, "herdmux: unknown command %q (run 'herdmux help')\n", args[0])
		return 2
	}
	return runVerb(v, args[1:], e)
}

func lookup(name string) *verb {
	for i := range verbs {
		if verbs[i].name == name {
			return &verbs[i]
		}
	}
	return nil
}

func runVerb(v *verb, args []string, e *env) (code int) {
	if v.neverFail {
		defer func() {
			if r := recover(); r != nil {
				_ = logfile.Printf(e.getenv, "%s: panic: %v", v.name, r)
			}
			code = 0
		}()
	}
	fs := flag.NewFlagSet("herdmux "+v.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fn := v.define(fs)
	pos, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		printVerbHelp(e.stdout, v, fs)
		return 0
	}
	if err == nil && v.needsTmux && e.getenv("TMUX") == "" {
		err = errNotInTmux
	}
	if err == nil {
		err = fn(e, pos)
	}
	if err == nil {
		return 0
	}
	if v.neverFail {
		_ = logfile.Printf(e.getenv, "%s %s: %v", v.name, strings.Join(args, " "), err)
		return 0
	}
	var ue usageError
	var se shownError
	switch {
	case errors.As(err, &se):
		return 1
	case errors.Is(err, errNotInTmux):
		fmt.Fprintln(e.stderr, "herdmux: not inside tmux")
		return 2
	case errors.As(err, &ue), isFlagError(err):
		fmt.Fprintf(e.stderr, "herdmux %s: %v\n", v.name, err)
		printVerbHelp(e.stderr, v, fs)
		return 2
	default:
		fmt.Fprintf(e.stderr, "herdmux %s: %v\n", v.name, err)
		return 1
	}
}

// flagError marks errors from flag parsing, so they exit 2.
type flagError struct{ error }

func isFlagError(err error) bool {
	var fe flagError
	return errors.As(err, &fe)
}

// parseInterspersed parses flags anywhere among the positional arguments
// (`report working --agent claude`), stopping at "--".
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, flagError{err}
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		// fs.Parse stops at the first non-flag, or after a "--" it consumed.
		consumed := len(args) - len(rest)
		if consumed > 0 && args[consumed-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func printHelp(w io.Writer, all bool) {
	fmt.Fprintf(w, "herdmux %s: an AI-agent awareness layer for tmux\n\n", version)
	fmt.Fprintln(w, "Usage: herdmux <command> [arguments]")
	fmt.Fprintln(w, "\nCommands:")
	printVerbs(w, false)
	if all {
		fmt.Fprintln(w, "\nHidden commands (called by tmux hooks and agent integrations):")
		printVerbs(w, true)
	}
	fmt.Fprintln(w, "\nRun 'herdmux <command> --help' for a command's flags, 'herdmux help --all' for")
	fmt.Fprintln(w, "hidden commands and 'herdmux --version' for the version.")
}

func printVerbs(w io.Writer, hidden bool) {
	for _, v := range verbs {
		if v.hidden != hidden {
			continue
		}
		use := strings.TrimSpace(v.name + " " + v.synopsis)
		if len(use) > 28 {
			fmt.Fprintf(w, "  %s\n  %-28s %s\n", use, "", v.summary)
			continue
		}
		fmt.Fprintf(w, "  %-28s %s\n", use, v.summary)
	}
}

func printVerbHelp(w io.Writer, v *verb, fs *flag.FlagSet) {
	fmt.Fprintf(w, "Usage: herdmux %s\n\n%s\n", strings.TrimSpace(v.name+" "+v.synopsis), v.summary)
	n := 0
	fs.VisitAll(func(*flag.Flag) { n++ })
	if n > 0 {
		fmt.Fprintln(w, "\nFlags:")
		fs.SetOutput(w)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
	}
}
