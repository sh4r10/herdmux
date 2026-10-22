// Package manpage holds herdmux(1), the full manual in man(7) roff, embedded
// in the binary. `herdmux man` shows it.
package manpage

import _ "embed"

//go:embed herdmux.1
var source string

// Page returns the roff source as embedded. It carries no version, so it
// never drifts from `herdmux --version`.
func Page() string { return source }
