package themes

import (
	"os/exec"
	"testing"
)

// The colour-mode script decides, before the page paints, whether a visitor sees light or dark.
// Its logic (saved choice, system setting, blocked storage) is checked against a fake browser.
// It needs Node.js; where that isn't installed the check is skipped.
func TestColorModeScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "testdata/colormode_check.js", "../../themes/default/assets/colormode.js").CombinedOutput()
	if err != nil {
		t.Fatalf("colour-mode script checks failed:\n%s", out)
	}
}
