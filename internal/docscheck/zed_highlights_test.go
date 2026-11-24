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

// editors/helix's generated queries and editors/nvim's are what
// scripts/sync-helix-runtime.sh and scripts/sync-nvim-runtime.sh generate
// from Zed's highlights, the canonical injections and Helix's own queries:
// `nomi editor install` writes them as they are committed, so a stale one
// would ship.
func TestHelixAndNeovimQueriesAreGenerated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the generators are bash scripts")
	}
	for _, tool := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the generators need %s", tool)
		}
	}
	for _, name := range []string{"sync-helix-runtime.sh", "sync-nvim-runtime.sh"} {
		script := filepath.Join("..", "..", "scripts", name)
		if out, err := exec.Command("bash", script, "--check").CombinedOutput(); err != nil {
			t.Errorf("%s --check: %v\n%s", script, err, out)
		}
	}
}
