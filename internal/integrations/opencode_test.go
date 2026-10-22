package integrations

import (
	"regexp"
	"testing"
)

func TestOpencodePluginEmbedded(t *testing.T) {
	if len(OpencodePlugin) == 0 {
		t.Fatal("opencode plugin not embedded")
	}
	for _, re := range []string{
		`(?m)^// HERDMUX_INTEGRATION_ID=opencode$`,
		`(?m)^// HERDMUX_INTEGRATION_VERSION=[0-9]+$`,
		`(?m)^export default \{`,
		`server: `,
	} {
		if !regexp.MustCompile(re).Match(OpencodePlugin) {
			t.Errorf("%s: no match for %s", OpencodePluginName, re)
		}
	}
	if regexp.MustCompile(`(?m)^\s*tui\s*[:(]`).Match(OpencodePlugin) {
		t.Errorf("%s must export server() only, not tui()", OpencodePluginName)
	}
}
