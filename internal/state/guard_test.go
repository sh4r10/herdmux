package state

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnlyStateNamesTheFiveOptions guards "no code path other than
// internal/state writes the five options": outside this package, shipped
// sources (not tests, not comments) may not spell the option names.
// Readers build their formats from the Opt* constants instead.
func TestOnlyStateNamesTheFiveOptions(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	exts := map[string]bool{".go": true, ".js": true, ".mjs": true, ".ts": true, ".sh": true, ".conf": true, ".json": true, ".toml": true}
	for _, dir := range []string{"cmd", "internal", "dev"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				if path == filepath.Join(root, "internal", "state") || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !exts[filepath.Ext(name)] || strings.Contains(name, "_test.") || strings.Contains(name, ".test.") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(b), "\n") {
				if l := strings.TrimSpace(line); strings.HasPrefix(l, "//") || strings.HasPrefix(l, "#") {
					continue // comments may mention them
				}
				for _, o := range AllOpts {
					if strings.Contains(line, o) {
						t.Errorf("%s:%d names %s; only internal/state may (use state.Opt* in formats)", path, i+1, o)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
