// Package mcp is the herdmux MCP server:
// `herdmux mcp` serves it over stdio on the official go-sdk. Its tools read
// and write config.toml whole, manage colorschemes (applied live to the tmux
// server in the caller's $TMUX) and list agents. It never prompts and never
// controls agents (no launch, stop, jump or client switch).
//
// Every path resolves under HERDMUX_CONFIG_DIR (config.Dir); there is no dev
// code path. Without $TMUX the live tools (set_colorscheme, list_agents)
// fail with ErrNoTmux, and the file tools still work.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"herdmux/internal/colorscheme"
	"herdmux/internal/config"
	"herdmux/internal/logfile"
	"herdmux/internal/state"
	"herdmux/internal/tmux"
)

// Name is the server name; agents show its tools as mcp__herdmux__<tool>.
const Name = "herdmux"

// Command is how every agent starts the server (dev shims, install --mcp).
var Command = []string{"herdmux", "mcp"}

// The tool names.
const (
	ReadConfig        = "read_config"
	WriteConfig       = "write_config"
	ListColorschemes  = "list_colorschemes"
	GetColorscheme    = "get_colorscheme"
	SetColorscheme    = "set_colorscheme"
	WriteColorscheme  = "write_colorscheme"
	DeleteColorscheme = "delete_colorscheme"
	ListAgents        = "list_agents"
)

// Tools lists every tool in registration order.
var Tools = []string{ReadConfig, WriteConfig, ListColorschemes, GetColorscheme,
	SetColorscheme, WriteColorscheme, DeleteColorscheme, ListAgents}

// ReadOnly lists the read-only tools: the ones claude pre-allows.
var ReadOnly = []string{ListColorschemes, ListAgents, GetColorscheme, ReadConfig}

// ClaudeAllow is the claude permissions.allow list pre-allowing the read
// tools (claude ignores readOnlyHint): mcp__herdmux__<tool> for ReadOnly.
func ClaudeAllow() []string {
	out := make([]string, len(ReadOnly))
	for i, t := range ReadOnly {
		out[i] = "mcp__" + Name + "__" + t
	}
	return out
}

// ErrNoTmux is the live tools' error outside tmux.
var ErrNoTmux = errors.New("not inside tmux ($TMUX is unset): this tool applies to the tmux server live; run the agent inside herdmux's tmux")

// ErrNotEnabled is Serve's error without HERDMUX=1.
var ErrNotEnabled = errors.New("HERDMUX=1 is not set: herdmux mcp only runs for an agent started inside a herdmux tmux server")

const instructions = `herdmux configures the tmux layer the user runs agents in.
config.toml (read_config / write_config) holds the launch menu ([[agent]] name, command, key), [keys] (picker, next-attention, organiser, launch, sessionizer, colorscheme → "prefix <key>", "<root key>" or "") and [sessionizer] (roots = absolute directories whose subdirectories the sessionizer lists as projects, depth = levels below each root, default 3). write_config replaces the whole file, is validated first, and rebinds [keys] live.
Colorschemes are TOML files with one [colors] table of 16 "#rrggbb" slots; set_colorscheme applies and saves one live, write_colorscheme saves a user scheme (a bundled name makes an override), delete_colorscheme removes a user file.
list_agents shows every agent pane; "caller" marks yours. Read before you write.`

// Server builds the MCP server. getenv supplies TMUX, TMUX_PANE,
// HERDMUX_CONFIG_DIR and HOME (pass os.Getenv, or a test's map).
func Server(getenv func(string) string) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: Name, Title: "herdmux"}, &sdk.ServerOptions{Instructions: instructions})
	h := &handlers{getenv: getenv}
	f, t := false, true
	read := &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &f}
	write := func(destructive, idempotent bool) *sdk.ToolAnnotations {
		d := &f
		if destructive {
			d = &t
		}
		return &sdk.ToolAnnotations{DestructiveHint: d, IdempotentHint: idempotent, OpenWorldHint: &f}
	}
	sdk.AddTool(s, &sdk.Tool{Name: ReadConfig, Annotations: read,
		Description: "Return the whole herdmux config.toml text (empty if there is no file: the defaults apply)."}, h.readConfig)
	sdk.AddTool(s, &sdk.Tool{Name: WriteConfig, Annotations: write(true, true),
		Description: "Replace the whole config.toml with text. It is validated with herdmux's parser first: any problem (bad TOML, unknown key, duplicate agent or key) rejects it and nothing is saved. On success it is saved and [keys] are rebound live."}, h.writeConfig)
	sdk.AddTool(s, &sdk.Tool{Name: ListColorschemes, Annotations: read,
		Description: "List every colorscheme: name, family, tone, source (bundled, user, or override: a user file shadowing a bundled scheme), saved, active, and validity."}, h.listColorschemes)
	sdk.AddTool(s, &sdk.Tool{Name: GetColorscheme, Annotations: read,
		Description: "Return a colorscheme's TOML text (the user file if one shadows the bundled scheme)."}, h.getColorscheme)
	sdk.AddTool(s, &sdk.Tool{Name: SetColorscheme, Annotations: write(false, true),
		Description: "Apply the named colorscheme live to the tmux server and save it as the choice. Unknown or invalid schemes are refused."}, h.setColorscheme)
	sdk.AddTool(s, &sdk.Tool{Name: WriteColorscheme, Annotations: write(true, true),
		Description: "Save text as the user colorscheme name (colors/<name>.toml). It must have a [colors] table with all 16 slots as \"#rrggbb\"; a bad scheme is refused and nothing is saved. A bundled name creates an override. Re-applied live if it is the active or saved scheme."}, h.writeColorscheme)
	sdk.AddTool(s, &sdk.Tool{Name: DeleteColorscheme, Annotations: write(true, true),
		Description: "Delete the user colorscheme file name. Bundled schemes can't be deleted; deleting an override brings the bundled scheme back. If it was in use, the effective scheme is re-applied."}, h.deleteColorscheme)
	sdk.AddTool(s, &sdk.Tool{Name: ListAgents, Annotations: read,
		Description: "List every agent pane across all tmux sessions: session, window, name, agent, state, attention and pane id. caller marks the pane this agent runs in."}, h.listAgents)
	return s
}

// Serve runs the server on r/w until the client hangs up. It refuses to
// start without HERDMUX=1.
func Serve(ctx context.Context, getenv func(string) string, r io.Reader, w io.Writer) error {
	if getenv("HERDMUX") != "1" {
		return ErrNotEnabled
	}
	var t sdk.Transport
	if r == os.Stdin && w == os.Stdout {
		t = &sdk.StdioTransport{}
	} else {
		t = &sdk.IOTransport{Reader: io.NopCloser(r), Writer: nopWriteCloser{w}}
	}
	err := Server(getenv).Run(ctx, t)
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type handlers struct{ getenv func(string) string }

type none struct{}

type nameArg struct {
	Name string `json:"name" jsonschema:"the colorscheme name (letters, digits, - and _)"`
}

type textArg struct {
	Text string `json:"text" jsonschema:"the whole config.toml text"`
}

type schemeArg struct {
	Name string `json:"name" jsonschema:"the colorscheme name (letters, digits, - and _)"`
	Text string `json:"text" jsonschema:"the whole scheme TOML: a [colors] table with the 16 slots as \"#rrggbb\""`
}

func text(s string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: s}}}
}

func jsonResult(v any) (*sdk.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return text(string(b)), nil, nil
}

// live returns a client for $TMUX, reconciled, or ErrNoTmux.
func (h *handlers) live() (*tmux.Client, error) {
	c, err := tmux.FromEnv(h.getenv)
	if errors.Is(err, tmux.ErrNoServer) {
		return nil, ErrNoTmux
	}
	if err != nil {
		return nil, err
	}
	return c, state.Reconcile(c)
}

func (h *handlers) log(format string, args ...any) {
	_ = logfile.Printf(h.getenv, "mcp: "+format, args...)
}

func (h *handlers) readConfig(_ context.Context, _ *sdk.CallToolRequest, _ none) (*sdk.CallToolResult, any, error) {
	p, err := config.Path(h.getenv)
	if err != nil {
		return nil, nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return text(""), nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return text(string(b)), nil, nil
}

func (h *handlers) writeConfig(_ context.Context, _ *sdk.CallToolRequest, in textArg) (*sdk.CallToolResult, any, error) {
	cfg, err := config.Write(h.getenv, []byte(in.Text))
	if err != nil {
		var ce *config.Error
		if errors.As(err, &ce) {
			return nil, nil, fmt.Errorf("rejected, nothing saved: %w", err)
		}
		return nil, nil, err
	}
	h.log("write_config saved")
	c, err := h.live()
	if errors.Is(err, ErrNoTmux) {
		return text("saved; not inside tmux, so [keys] apply at the next herdmux init"), nil, nil
	}
	if err == nil {
		err = config.Bind(c, cfg.Keys)
	}
	if err != nil {
		h.log("write_config rebind: %v", err)
		return nil, nil, fmt.Errorf("saved, but rebinding [keys] live failed: %w", err)
	}
	return text("saved; [keys] rebound live"), nil, nil
}

// SchemeInfo is one list_colorschemes row.
type SchemeInfo struct {
	Name   string `json:"name"`
	Family string `json:"family"`
	Tone   string `json:"tone,omitempty"` // dark | light; "" when invalid
	Source string `json:"source"`         // bundled | user | override
	Saved  bool   `json:"saved"`          // the saved choice
	// Active is the scheme the tmux server shows (inside tmux), else the
	// one that would be applied for the saved choice.
	Active  bool     `json:"active"`
	Valid   bool     `json:"valid"`
	Reason  string   `json:"reason,omitempty"`  // why it's invalid
	Ignored []string `json:"ignored,omitempty"` // keys that aren't slots
}

func (h *handlers) store() (*colorscheme.Store, error) { return colorscheme.FromEnv(h.getenv) }

// active is the scheme the server shows, or (outside tmux) the effective one.
func (h *handlers) active(st *colorscheme.Store) string {
	if c, err := h.live(); err == nil { // live reconciles, as every herdmux run does
		if a, err := colorscheme.Active(c); err == nil && a != "" {
			return a
		}
	}
	if r, err := st.Resolve(); err == nil {
		return r.Scheme.Name
	}
	return ""
}

func (h *handlers) listColorschemes(_ context.Context, _ *sdk.CallToolRequest, _ none) (*sdk.CallToolResult, any, error) {
	st, err := h.store()
	if err != nil {
		return nil, nil, err
	}
	all, err := st.List()
	if err != nil {
		return nil, nil, err
	}
	active := h.active(st)
	out := make([]SchemeInfo, 0, len(all))
	for _, s := range all {
		i := SchemeInfo{Name: s.Name, Family: s.Family, Source: string(s.Source), Saved: s.Saved,
			Active: s.Name == active, Valid: s.Valid(), Reason: s.Reason(), Ignored: s.Ignored}
		if i.Valid {
			i.Tone = s.Tone()
		}
		out = append(out, i)
	}
	return jsonResult(out)
}

func (h *handlers) getColorscheme(_ context.Context, _ *sdk.CallToolRequest, in nameArg) (*sdk.CallToolResult, any, error) {
	st, err := h.store()
	if err != nil {
		return nil, nil, err
	}
	b, err := st.Raw(in.Name)
	if err != nil {
		return nil, nil, err
	}
	return text(string(b)), nil, nil
}

func (h *handlers) setColorscheme(_ context.Context, _ *sdk.CallToolRequest, in nameArg) (*sdk.CallToolResult, any, error) {
	st, err := h.store()
	if err != nil {
		return nil, nil, err
	}
	c, err := h.live()
	if err != nil {
		return nil, nil, err
	}
	if _, err := colorscheme.Set(c, st, in.Name); err != nil {
		return nil, nil, err
	}
	h.log("set_colorscheme %s", in.Name)
	return text("colorscheme " + in.Name + " applied and saved"), nil, nil
}

// reapply re-applies the effective scheme when name is the one the server
// shows or the saved choice. It returns a note for the result.
func (h *handlers) reapply(st *colorscheme.Store, name string) (string, error) {
	c, err := h.live()
	if errors.Is(err, ErrNoTmux) {
		return "; not inside tmux, nothing applied live", nil
	}
	if err != nil {
		return "", err
	}
	active, err := colorscheme.Active(c)
	if err != nil {
		return "", err
	}
	saved, err := st.Saved()
	if err != nil {
		return "", err
	}
	if name != active && name != saved {
		return "", nil
	}
	r, err := colorscheme.ApplySaved(c, st)
	if err != nil {
		return "", err
	}
	note := "; re-applied " + r.Scheme.Name + " live"
	if r.Warning != "" {
		note += " (" + r.Warning + ")"
	}
	return note, nil
}

func (h *handlers) writeColorscheme(_ context.Context, _ *sdk.CallToolRequest, in schemeArg) (*sdk.CallToolResult, any, error) {
	st, err := h.store()
	if err != nil {
		return nil, nil, err
	}
	s, err := st.Write(in.Name, []byte(in.Text))
	if err != nil {
		return nil, nil, fmt.Errorf("rejected, nothing saved: %w", err)
	}
	h.log("write_colorscheme %s", in.Name)
	msg := "saved " + s.File + " (" + string(s.Source) + ")"
	if len(s.Ignored) > 0 {
		msg += "; ignored keys: " + strings.Join(s.Ignored, ", ")
	}
	note, err := h.reapply(st, in.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("%s, but re-applying failed: %w", msg, err)
	}
	return text(msg + note), nil, nil
}

func (h *handlers) deleteColorscheme(_ context.Context, _ *sdk.CallToolRequest, in nameArg) (*sdk.CallToolResult, any, error) {
	st, err := h.store()
	if err != nil {
		return nil, nil, err
	}
	if err := st.Delete(in.Name); err != nil {
		return nil, nil, err
	}
	h.log("delete_colorscheme %s", in.Name)
	msg := "deleted " + st.UserPath(in.Name)
	if slices.Contains(colorscheme.BundledNames, in.Name) {
		msg += "; the bundled " + in.Name + " is back"
	}
	note, err := h.reapply(st, in.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("%s, but re-applying failed: %w", msg, err)
	}
	return text(msg + note), nil, nil
}

// AgentInfo is one list_agents row: state.Agent's JSON plus "caller".
type AgentInfo struct {
	state.Agent
	Caller bool `json:"caller"` // the pane in the caller's $TMUX_PANE
}

func (h *handlers) listAgents(_ context.Context, _ *sdk.CallToolRequest, _ none) (*sdk.CallToolResult, any, error) {
	c, err := h.live()
	if err != nil {
		return nil, nil, err
	}
	agents, err := state.List(c)
	if err != nil {
		return nil, nil, err
	}
	me := h.getenv("TMUX_PANE")
	out := make([]AgentInfo, len(agents))
	for i, a := range agents {
		out[i] = AgentInfo{Agent: a, Caller: me != "" && a.Pane == me}
	}
	return jsonResult(out)
}
