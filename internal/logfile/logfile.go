// Package logfile is herdmux's shared log, used by the verbs that must never
// fail (hook, report, tmux-hook) and by anything else that wants a record.
//
// The log is $XDG_STATE_HOME/herdmux/log when XDG_STATE_HOME is set to an
// absolute path, and ~/.local/state/herdmux/log otherwise.
package logfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Path returns the log file path, reading the environment through getenv.
func Path(getenv func(string) string) (string, error) {
	if x := getenv("XDG_STATE_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "herdmux", "log"), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("logfile: HOME is unset")
	}
	return filepath.Join(home, ".local", "state", "herdmux", "log"), nil
}

// Printf appends one timestamped line to the log. It creates the directory
// if needed. The returned error is for callers that can report it; the
// never-fail verbs ignore it.
func Printf(getenv func(string) string, format string, args ...any) error {
	p, err := Path(getenv)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	msg := strings.TrimRight(fmt.Sprintf(format, args...), "\n")
	_, err = fmt.Fprintf(f, "%s pid=%d %s\n", time.Now().Format(time.RFC3339), os.Getpid(), msg)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
