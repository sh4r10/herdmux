package integrations

import _ "embed"

// PiExtension is the pi agent-state extension. Dev launches load it per
// launch with `pi -e` (dev/shims/pi); `herdmux install` installs it as
// PiExtensionName under ~/.pi/agent/extensions/. It never registers MCP.
//
//go:embed pi/herdmux-agent-state.ts
var PiExtension []byte

// PiExtensionName is the installed file name.
const PiExtensionName = "herdmux-agent-state.ts"
