package themes

import (
	"os/exec"
	"testing"
)

// The cookie-notice script decides whether a visitor is asked and what answer other features see.
// Its logic (saved answers, a changed notice, blocked storage, reopening) is checked against a fake
// browser. It needs Node.js; where that isn't installed the check is skipped.
func TestConsentScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "testdata/consent_check.js", "../../themes/default/assets/consent.js").CombinedOutput()
	if err != nil {
		t.Fatalf("cookie-notice script checks failed:\n%s", out)
	}
}
