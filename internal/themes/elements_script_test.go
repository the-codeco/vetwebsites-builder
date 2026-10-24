package themes

import (
	"os/exec"
	"testing"
)

// The site-wide elements script decides when a notice, popup or button is on screen, using the visitor's own clock,
// because a published website has no server to do it. Its logic (windows in time, closing and remembering, version
// changes, triggers, blocked storage) is checked against a fake browser with a clock under the test's control. It needs
// Node.js; where that isn't installed the check is skipped.
func TestElementsScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "testdata/elements_check.js", "../../themes/default/assets/elements.js").CombinedOutput()
	if err != nil {
		t.Fatalf("site-wide elements script checks failed:\n%s", out)
	}
}
