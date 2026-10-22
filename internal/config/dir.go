package config

import (
	"errors"
	"path/filepath"
)

// Dir is herdmux's config directory: $HERDMUX_CONFIG_DIR if set, else
// $XDG_CONFIG_HOME/herdmux (XDGConfigHome: a relative XDG_CONFIG_HOME is
// ignored), else ~/.config/herdmux. Every config path (config.toml,
// colorscheme, colors/) lives under it. Pass the verb's getenv, never
// os.Getenv, so tests stay in their temp dirs.
func Dir(getenv func(string) string) (string, error) {
	if d := getenv("HERDMUX_CONFIG_DIR"); d != "" {
		return d, nil
	}
	x, err := XDGConfigHome(getenv)
	if err != nil {
		return "", errors.New("neither HERDMUX_CONFIG_DIR, an absolute XDG_CONFIG_HOME nor HOME is set")
	}
	return filepath.Join(x, "herdmux"), nil
}

// XDGConfigHome is the XDG base directory for config files: $XDG_CONFIG_HOME
// if it is an absolute path (the spec says to ignore a relative one), else
// $HOME/.config. It is an error when neither is usable. herdmux's own config
// dir and install's tmux.conf and opencode paths all resolve through it.
func XDGConfigHome(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Clean(x), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("neither an absolute XDG_CONFIG_HOME nor HOME is set")
	}
	return filepath.Join(home, ".config"), nil
}
