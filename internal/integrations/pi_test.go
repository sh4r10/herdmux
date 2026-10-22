package integrations

import (
	"bytes"
	"regexp"
	"testing"
)

func TestPiExtensionEmbedded(t *testing.T) {
	if len(PiExtension) == 0 {
		t.Fatal("pi extension not embedded")
	}
	for _, re := range []string{
		`(?m)^// HERDMUX_INTEGRATION_ID: \S+$`,
		`(?m)^// HERDMUX_INTEGRATION_VERSION: \S+$`,
	} {
		if !regexp.MustCompile(re).Match(PiExtension) {
			t.Errorf("pi extension lacks the header %s", re)
		}
	}
	if !bytes.Contains(PiExtension, []byte(`env.PI_SUBAGENT_CHILD === "1"`)) {
		t.Error("pi extension lacks the PI_SUBAGENT_CHILD gate")
	}
}
