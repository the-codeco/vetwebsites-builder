package themes

import (
	"os/exec"
	"testing"
)

// The tabs script turns a block's titled sections into keyboard-operable tabs. Its logic is checked
// against a fake page. It needs Node.js; where that isn't installed the check is skipped.
func TestTabsScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "testdata/tabs_check.js", "../../themes/default/assets/menu.js").CombinedOutput()
	if err != nil {
		t.Fatalf("tabs script checks failed:\n%s", out)
	}
}
