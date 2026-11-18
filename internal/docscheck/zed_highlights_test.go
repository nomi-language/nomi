package docscheck

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// editors/zed/languages/nomi/highlights.scm is what
// scripts/sync-zed-highlights.sh generates from the canonical query
// (tree-sitter-nomi/queries/highlights.scm) and the hand-written
// attached-test dim: nobody edited it, and nobody changed the canonical
// query without regenerating it.
func TestZedHighlightsAreGenerated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the generator is a bash script")
	}
	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the generator needs %s", tool)
		}
	}
	script := filepath.Join("..", "..", "scripts", "sync-zed-highlights.sh")
	if out, err := exec.Command("bash", script, "--check").CombinedOutput(); err != nil {
		t.Errorf("%s --check: %v\n%s", script, err, out)
	}
}
