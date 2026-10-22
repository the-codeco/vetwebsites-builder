package admin

import (
	"os/exec"
	"testing"
)

// The image editor's crop box is plain geometry (dragging, resizing, keeping a shape, staying inside the
// picture), checked here without a browser. It needs Node.js; where that isn't installed the check is skipped.
func TestImageEditScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "testdata/imageedit_check.js", "../../web/static/imageedit.js").CombinedOutput()
	if err != nil {
		t.Fatalf("image editor checks failed:\n%s", out)
	}
}
