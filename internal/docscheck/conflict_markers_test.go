package docscheck

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A conflict marker is a line that starts with one of git's seven-character
// markers followed by a space (git writes a label after the opening, base and
// closing markers) or that is the bare marker. `=======` alone is also a
// Setext heading underline in Markdown, so it counts only between an opening
// `<<<<<<<` and its closing `>>>>>>>`.
const (
	markOpen  = "<<<<<<<"
	markBase  = "|||||||"
	markSep   = "======="
	markClose = ">>>>>>>"
)

func isMarker(line, mark string) bool {
	return line == mark || strings.HasPrefix(line, mark+" ")
}

// conflictMarkers answers the 1-based line numbers of every conflict marker
// in src.
func conflictMarkers(src []byte) []int {
	var lines []int
	open := false
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case isMarker(line, markOpen):
			open = true
			lines = append(lines, n)
		case isMarker(line, markBase), isMarker(line, markClose):
			if isMarker(line, markClose) {
				open = false
			}
			lines = append(lines, n)
		case line == markSep && open:
			lines = append(lines, n)
		}
	}
	return lines
}

func TestConflictMarkers_TheScannerFindsEachMarkerAndSparesSetextHeadings(t *testing.T) {
	src := strings.Join([]string{
		"Title",
		"=======",
		"",
		"<<<<<<< HEAD",
		"ours",
		"||||||| base",
		"base",
		"=======",
		"theirs",
		">>>>>>> branch",
		"",
		"Another title",
		"=======",
		"<<<<<<<",
		">>>>>>>",
	}, "\n")
	got := conflictMarkers([]byte(src))
	want := []int{4, 6, 8, 10, 14, 15}
	if len(got) != len(want) {
		t.Fatalf("markers at %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("markers at %v, want %v", got, want)
		}
	}
}

// TestConflictMarkers_NoTrackedMarkdownOrGoFileHasOne scans every tracked
// .md and .go file in the repository.
func TestConflictMarkers_NoTrackedMarkdownOrGoFileHasOne(t *testing.T) {
	root, files := trackedFiles(t)
	if len(files) < 100 {
		t.Fatalf("found %d tracked .md/.go files under %s; the scan is not reading the repository", len(files), root)
	}
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			if os.IsNotExist(err) {
				continue // deleted in the working tree, not yet committed
			}
			t.Fatal(err)
		}
		for _, n := range conflictMarkers(src) {
			t.Errorf("%s:%d: git conflict marker", rel, n)
		}
	}
}

// trackedFiles answers the repository root and its tracked .md and .go files,
// relative to it.
func trackedFiles(t *testing.T) (string, []string) {
	t.Helper()
	return trackedFilesMatching(t, "*.md", "*.go")
}

// trackedFilesMatching answers the repository root and its tracked files
// matching any of the git pathspecs, relative to it.
func trackedFilesMatching(t *testing.T, pathspecs ...string) (string, []string) {
	t.Helper()
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	root := strings.TrimSpace(string(top))
	args := append([]string{"-C", root, "ls-files", "-z", "--"}, pathspecs...)
	out, err := exec.Command("git", args...).Output()
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
