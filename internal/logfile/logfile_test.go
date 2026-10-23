package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathAndPrintf(t *testing.T) {
	t.Parallel()
	home, state := t.TempDir(), t.TempDir()
	cases := []struct {
		vars map[string]string
		want string
	}{
		{map[string]string{"HOME": home}, filepath.Join(home, ".local/state/herdmux/log")},
		{map[string]string{"HOME": home, "XDG_STATE_HOME": "relative"}, filepath.Join(home, ".local/state/herdmux/log")},
		{map[string]string{"HOME": home, "XDG_STATE_HOME": state}, filepath.Join(state, "github.com/sh4r10/herdmux/log")},
	}
	for _, c := range cases {
		getenv := func(k string) string { return c.vars[k] }
		if got, _ := Path(getenv); got != c.want {
			t.Errorf("Path(%v) = %q, want %q", c.vars, got, c.want)
		}
		if err := Printf(getenv, "hello %d\n", 1); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(c.want)
		if !strings.HasSuffix(string(b), " hello 1\n") {
			t.Errorf("log = %q", b)
		}
	}
	if _, err := Path(func(string) string { return "" }); err == nil {
		t.Error("Path with no HOME succeeded")
	}
}
