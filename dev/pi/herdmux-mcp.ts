// herdmux-dev only: registers the herdmux MCP server for this pi session.
// dev/shims/pi loads it with -e; it is never installed (the installed
// herdmux-agent-state.ts never registers MCP: once installed, MCP is opt-in
// via `herdmux install --mcp`). The registration lasts for the session only;
// nothing is written to ~/.pi.
//
// exposure "direct" makes the tools ordinary model tools (the default,
// "codemode", reaches them from scripts only). pi runs every tool unprompted.
// Plain JavaScript syntax, like the state extension.

export default function herdmuxMcpDev(pi) {
  pi.registerMcpServer("herdmux", { command: "herdmux", args: ["mcp"], env: {}, exposure: "direct" });
}
