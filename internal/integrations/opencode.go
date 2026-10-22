package integrations

import _ "embed"

// OpencodePluginName is the opencode server plugin's file name. Install
// writes it to ~/.config/opencode/plugins/; the herdmux-dev
// opencode shim copies it into its OPENCODE_CONFIG_DIR's plugins/.
const OpencodePluginName = "herdmux-agent-state.js"

// OpencodePlugin is the opencode server plugin. Its header
// carries HERDMUX_INTEGRATION_ID and HERDMUX_INTEGRATION_VERSION comments;
// bump the version whenever the file changes.
//
//go:embed opencode/herdmux-agent-state.js
var OpencodePlugin []byte
