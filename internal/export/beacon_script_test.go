package export

import (
	"os/exec"
	"testing"
)

// The visit-counting script is checked against a fake browser. It needs Node.js; where that isn't installed the
// check is skipped.
func TestBeaconScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "testdata/beacon_check.js", "assets/beacon.js").CombinedOutput()
	if err != nil {
		t.Fatalf("beacon script checks failed:\n%s", out)
	}
}
