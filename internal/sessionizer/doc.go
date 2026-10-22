// Package sessionizer lists directories from configured roots and merges them
// with open tmux sessions, allowing quick switching or launching new sessions
// by project directory (the tmux-sessionizer pattern).
//
// Directories are walked to a configurable depth, filtered by substring match,
// and presented with open sessions prioritized.
package sessionizer
