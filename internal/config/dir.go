package config

import (
	"errors"
	"path/filepath"
)

// Dir is herdmux's config directory: $HERDMUX_CONFIG_DIR if set, else
// ~/.config/herdmux. Every config path (config.toml,
// colorscheme, colors/) lives under it. Pass the verb's getenv, never
// os.Getenv, so tests stay in their temp dirs.
func Dir(getenv func(string) string) (string, error) {
	if d := getenv("HERDMUX_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("neither HERDMUX_CONFIG_DIR nor HOME is set")
	}
	return filepath.Join(home, ".config", "herdmux"), nil
}
