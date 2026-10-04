package format

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// unformattedAllowed lists the tracked .nomi files that stay as written
// because a test depends on the spelling `nomi fmt` would change. Each entry
// needs a reason; a file that is formatter-clean must not be listed.
var unformattedAllowed = map[string]string{
	"internal/irbuild/testdata/dbg_shapes.nomi": "runs the keyword-prefixed stage `5 |> dbg double()`, which nomi fmt rewrites to `5 |> double() |> dbg`",
	"internal/irbuild/testdata/normalize.nomi":  "spells composed and decomposed strings as `\\u{...}` escapes, which nomi fmt decodes into identical-looking text",
}

// TestTrackedNomiFilesAreFormatted formats every tracked .nomi file and fails
// for each one whose formatted text differs from what is committed.
func TestTrackedNomiFilesAreFormatted(t *testing.T) {
	root, files := trackedNomiFiles(t)
	if len(files) < 100 {
		t.Fatalf("found %d tracked .nomi files under %s; the scan is not reading the repository", len(files), root)
	}
	tracked := map[string]bool{}
	for _, rel := range files {
		tracked[rel] = true
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue // deleted in the working tree, not yet committed
			}
			t.Fatal(err)
		}
		out, err := Format(string(src))
		_, allowed := unformattedAllowed[rel]
		switch {
		case err != nil && !allowed:
			t.Errorf("%s does not parse, so nomi fmt cannot format it: %v", rel, err)
		case err == nil && out != string(src) && !allowed:
			t.Errorf("%s is not formatted; from the repository root run `nomi fmt -w %s` "+
				"(or, if a test depends on its exact layout, add it to unformattedAllowed with the reason)", rel, rel)
		case err == nil && out == string(src) && allowed:
			t.Errorf("%s is formatter-clean; remove it from unformattedAllowed", rel)
		}
	}
	for rel := range unformattedAllowed {
		if !tracked[rel] {
			t.Errorf("unformattedAllowed lists %s, which is not a tracked .nomi file", rel)
		}
	}
}

// trackedNomiFiles answers the repository root and its tracked .nomi files,
// relative to it.
func trackedNomiFiles(t *testing.T) (string, []string) {
	t.Helper()
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	root := strings.TrimSpace(string(top))
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--", "*.nomi").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return root, files
}
