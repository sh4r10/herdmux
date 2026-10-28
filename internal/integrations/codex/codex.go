// Package codex is the Codex CLI integration: the embedded hooks file.
// codex's hooks speak Claude Code's hook protocol (the same event names and
// stdin JSON), so the event mapping is internal/integrations/claude's, run
// with the agent name "codex" by `herdmux hook codex` (cmd/herdmux).
//
// codex runs hooks from ~/.codex/hooks.json only after the user has trusted
// them once with /hooks inside codex (it records trust against the file's
// hash); install says so. Hooks are on by default ([features] hooks).
package codex

import _ "embed"

// Command is the exact command of every herdmux hook entry. install merges
// the Hooks entries into ~/.codex/hooks.json and uninstall removes exactly
// the entries with this command.
const Command = "herdmux hook codex"

// Hooks is the codex hooks.json whose entries report state: one synchronous
// Command per event (SessionStart, UserPromptSubmit, PreToolUse,
// PostToolUse, PermissionRequest, Stop, Interrupt, SessionEnd). The dev shim
// (dev/shims/codex) passes the same entries per launch as -c overrides.
// Keep it the one source, hooks only.
//
//go:embed hooks.json
var Hooks []byte
