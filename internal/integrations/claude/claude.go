// Package claude is the Claude Code integration: the embedded hooks settings
// file, and the mapping from one hook event (the JSON claude writes on the
// hook's stdin) to a state report. `herdmux hook claude` (cmd/herdmux) wires
// the two to internal/state.
//
// codex speaks the same hook protocol (the same event names and stdin JSON),
// so `herdmux hook codex` reuses Parse, Decide, Handle and Find with the
// agent name "codex"; only its hooks file lives elsewhere
// (internal/integrations/codex).
package claude

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/sh4r10/herdmux/internal/state"
	"github.com/sh4r10/herdmux/internal/tmux"
)

// Command is the exact command of every herdmux hook entry. install merges
// the Settings hooks into ~/.claude/settings.json and uninstall removes
// exactly the entries with this command.
const Command = "herdmux hook claude"

// Settings is the claude settings JSON whose "hooks" report state. The dev
// shim (dev/shims/claude) passes dev/claude/settings.json per launch: these
// hooks plus the MCP read pre-allows (kept in sync by a test in
// internal/mcp). Install merges the same entries into the user's settings.
// Keep it the one source, hooks only.
//
//go:embed settings.json
var Settings []byte

// Event is the part of a hook's stdin JSON that decides the state.
type Event struct {
	Name             string            `json:"hook_event_name"`
	Source           string            `json:"source"`            // SessionStart: startup|resume|clear|compact|fork
	Reason           string            `json:"reason"`            // SessionEnd
	NotificationType string            `json:"notification_type"` // Notification
	ToolName         string            `json:"tool_name"`         // PreToolUse, PostToolUse, PermissionRequest
	AgentID          string            `json:"agent_id"`          // set on subagent and internal-agent events
	BackgroundTasks  []json.RawMessage `json:"background_tasks"`  // Stop: work still in flight
}

// Parse reads one hook event. It fails on malformed JSON or a missing
// hook_event_name.
func Parse(b []byte) (Event, error) {
	var ev Event
	if err := json.Unmarshal(b, &ev); err != nil {
		return Event{}, err
	}
	if ev.Name == "" {
		return Event{}, errors.New("hook JSON has no hook_event_name")
	}
	return ev, nil
}

// Action is what an event does to the pane's agent.
type Action int

const (
	Ignore     Action = iota // no state change
	Register                 // SessionStart: register as done, never raising Attention
	Working                  // report working
	NeedsInput               // report needs-input
	Done                     // report done
	Exit                     // report exit: the agent leaves the pane
)

func (a Action) String() string {
	return [...]string{"ignore", "register", "working", "needs-input", "done", "exit"}[a]
}

// Decide maps a main-thread event to its Action:
//
//   - Any event carrying agent_id (subagents, and the internal agents that
//     fire even after Stop), and SubagentStart/SubagentStop: ignored.
//   - SessionStart (any source): Register.
//   - UserPromptSubmit, PreToolUse, PostToolUse, PostToolBatch: Working.
//     PostToolUse marks the return from needs-input after an approval.
//     PreToolUse of codex's request_user_input (its question to the user,
//     which asks for no permission) is NeedsInput instead.
//   - PermissionRequest (permissions and AskUserQuestion), and Notification
//     permission_prompt / elicitation_dialog / elicitation_url_dialog (a late
//     backup): NeedsInput.
//   - Stop with an empty background_tasks, and StopFailure: Done. Stop with
//     background work still running is ignored: the agent stays working.
//     Interrupt (codex: the user cut the turn short) is Done too.
//   - SessionEnd prompt_input_exit / logout / other: Exit. clear and resume
//     are ignored (a new SessionStart follows).
//   - Everything else (idle_prompt and other notifications, unknown events):
//     ignored.
func Decide(ev Event) Action {
	if ev.AgentID != "" {
		return Ignore
	}
	switch ev.Name {
	case "SessionStart":
		return Register
	case "PreToolUse":
		if ev.ToolName == "request_user_input" {
			return NeedsInput
		}
		return Working
	case "UserPromptSubmit", "PostToolUse", "PostToolBatch":
		return Working
	case "PermissionRequest":
		return NeedsInput
	case "Notification":
		switch ev.NotificationType {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog":
			return NeedsInput
		}
	case "Stop":
		if len(ev.BackgroundTasks) == 0 {
			return Done
		}
	case "StopFailure", "Interrupt":
		return Done
	case "SessionEnd":
		switch ev.Reason {
		case "prompt_input_exit", "logout", "other":
			return Exit
		}
	}
	return Ignore
}

// Handle applies one event to the pane's agent ("claude" or "codex") with
// state.Apply. pid is the agent process (Find). The caller reconciles first.
// The Outcome is the final Apply's, for the cross-session bell.
//
// Register reports done without ever raising Attention. On a pane with no
// such agent, or an unknown one, that is a plain registration. An agent
// already at done (a SessionStart after /clear or /resume at the prompt)
// stays done and keeps its Attention. One in working or needs-input is
// removed and registered afresh, so the change to done can't raise
// Attention; but a SessionStart from compaction there is ignored, since
// compaction can happen mid-turn and the turn goes on.
func Handle(c *tmux.Client, agent, pane string, pid int, ev Event) (state.Outcome, error) {
	a := Decide(ev)
	if a == Ignore {
		return state.Outcome{}, nil
	}
	r := state.Report{Pane: pane, Agent: agent, PID: pid}
	switch a {
	case Register:
		out, err := c.Display(pane, tmux.Fields("#{"+state.OptAgent+"}", "#{"+state.OptState+"}"))
		if err != nil {
			return state.Outcome{}, err
		}
		f := tmux.Split(out)
		if len(f) == 2 && f[0] == agent && f[1] != "" && f[1] != state.Unknown && f[1] != state.Done {
			if ev.Source == "compact" {
				return state.Outcome{}, nil
			}
			if _, err := state.Apply(c, state.Report{Pane: pane, Agent: agent, Event: state.Exit}); err != nil {
				return state.Outcome{}, err
			}
		}
		r.Event = state.Done
	case Working:
		r.Event = state.Working
	case NeedsInput:
		r.Event = state.NeedsInput
	case Done:
		r.Event = state.Done
	case Exit:
		r.Event = state.Exit
	}
	return state.Apply(c, r)
}
