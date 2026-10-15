package analysis_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/analysis"
)

// diskProject is layeredProject written to disk with only its entry open in
// a language server's document manager: an analysis of the entry compares
// every other file, closed, with the text the kept program was checked
// from. Every file was last modified at, unless a test changes it.
type diskProject struct {
	t    *testing.T
	dir  string
	dm   *analysis.DocumentManager
	main string
	n    int
}

func openDiskProject(t *testing.T, at time.Time) *diskProject {
	p := layeredProject()
	d := &diskProject{t: t, dir: writeProject(t, p), dm: newManager(), main: p.texts["main"]}
	backdate(t, d.dir, at)
	d.dm.Open(d.uri("main"), d.main)
	return d
}

func (d *diskProject) path(key string) string {
	return filepath.Join(d.dir, filepath.FromSlash(key)+".nomi")
}

func (d *diskProject) uri(key string) string { return "file://" + d.path(key) }

// write replaces key's file with text, last modified at.
func (d *diskProject) write(key, text string, at time.Time) {
	d.t.Helper()
	if err := os.WriteFile(d.path(key), []byte(text), 0o644); err != nil {
		d.t.Fatal(err)
	}
	if err := os.Chtimes(d.path(key), at, at); err != nil {
		d.t.Fatal(err)
	}
}

// edit edits the entry's body, which makes the open entry's analysis run,
// and answers the files it read from disk, by module key, and the entry's
// diagnostics, which must be what a fresh analysis of the entry, reading
// every file from disk, gives.
func (d *diskProject) edit() (read []string, diagnostics string) {
	d.t.Helper()
	read, got, text := d.editOnly()
	if want := renderBuild(analysis.BuildForTest(newManager(), d.uri("main"), text, false)); got != want {
		d.t.Fatalf("the entry gets\n%s\nand a fresh analysis of it gives\n%s", got, want)
	}
	return read, got
}

// editOnly is edit without the comparison, also answering the entry's text.
func (d *diskProject) editOnly() (read []string, diagnostics, text string) {
	d.t.Helper()
	d.n++
	text = strings.Replace(d.main, `"anvil"`, `"anvil `+strings.Repeat("!", d.n)+`"`, 1)
	read = d.reads(func() { d.dm.Update(d.uri("main"), text) })
	return read, renderBuild(analysis.InstalledForTest(d.dm, d.uri("main"))), text
}

// reads runs f and answers the files it read from disk, by module key.
func (d *diskProject) reads(f func()) []string {
	var read []string
	for _, p := range analysis.DiskReadsForTest(f) {
		read = append(read, strings.TrimSuffix(strings.TrimPrefix(p, d.dir+string(filepath.Separator)), ".nomi"))
	}
	slices.Sort(read)
	return read
}

// TestClosedFilesAreReadOnlyWhenTheirStatChanges: an analysis of an open
// document reads a closed file of its program from disk only when the
// file's stat (size, modification time, inode) differs from when its text
// was read, or was racy then; and it notices every change that way.
func TestClosedFilesAreReadOnlyWhenTheirStatChanges(t *testing.T) {
	old := time.Now().Add(-time.Hour)

	t.Run("an untouched file is not read", func(t *testing.T) {
		d := openDiskProject(t, old)
		for range 3 {
			if read, _ := d.edit(); len(read) > 0 {
				t.Fatalf("the analysis read %v, which did not change", read)
			}
		}
	})

	t.Run("an edited file is read", func(t *testing.T) {
		d := openDiskProject(t, old)
		d.write("palette", strings.Replace(layeredProject().texts["palette"], `"red"`, `"green"`, 1), old.Add(time.Minute))
		read, _ := d.edit()
		if !slices.Equal(read, []string{"palette"}) {
			t.Fatalf("the analysis read %v, want palette alone", read)
		}
		if read, _ := d.edit(); len(read) > 0 {
			t.Fatalf("the next analysis read %v again", read)
		}
	})

	// The same size and the same modification time, as a second write
	// within one timestamp tick leaves them: the stat cannot tell.
	sameStat := strings.Replace(layeredProject().texts["palette"], "pub fn name(): String {", "pub fn nome(): String {", 1)

	t.Run("a rewrite within the racy window is noticed", func(t *testing.T) {
		// Written just now: the stat the first analysis takes is racy.
		now := time.Now()
		d := openDiskProject(t, now)
		d.write("palette", sameStat, now)
		read, diags := d.edit()
		if !slices.Contains(read, "palette") {
			t.Fatalf("the analysis read %v, not palette", read)
		}
		if !strings.Contains(diags, "name") {
			t.Fatalf("the entry's call of palette.name is not reported:\n%s", diags)
		}
	})

	t.Run("an old stat is trusted", func(t *testing.T) {
		// The rule the window exists for: outside it, a write that keeps
		// the size, the modification time and the inode goes unseen.
		d := openDiskProject(t, old)
		d.write("palette", sameStat, old)
		if read, diags, _ := d.editOnly(); len(read) > 0 || strings.Contains(diags, "diag") {
			t.Fatalf("the analysis read %v and reports\n%s", read, diags)
		}
	})

	t.Run("a file replaced by a rename is read", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("no inode in the stat")
		}
		d := openDiskProject(t, old)
		tmp := filepath.Join(d.dir, "palette.new")
		if err := os.WriteFile(tmp, []byte(sameStat), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(tmp, old, old); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, d.path("palette")); err != nil {
			t.Fatal(err)
		}
		read, diags := d.edit()
		if !slices.Equal(read, []string{"palette"}) || !strings.Contains(diags, "name") {
			t.Fatalf("the analysis read %v and reports\n%s", read, diags)
		}
	})

	t.Run("a deleted file is noticed", func(t *testing.T) {
		d := openDiskProject(t, old)
		if err := os.Remove(d.path("palette")); err != nil {
			t.Fatal(err)
		}
		_, _, full, _ := d.dm.ProgramChecks()
		if _, diags := d.edit(); !strings.Contains(diags, "palette") {
			t.Fatalf("the entry's import of the deleted palette is not reported:\n%s", diags)
		}
		if _, _, after, _ := d.dm.ProgramChecks(); after != full+1 || d.dm.FullCheckReasons()["a file is gone"] != 1 {
			t.Fatalf("want one full check because a file is gone, got %v", d.dm.FullCheckReasons())
		}
	})

	t.Run("a new file is noticed", func(t *testing.T) {
		d := openDiskProject(t, old)
		d.main = strings.Replace(d.main, "    palette\n}", "    palette\n    extra\n}", 1)
		d.main = strings.Replace(d.main, "${palette.name()}", "${palette.name()} ${extra.more()}", 1)
		if _, diags := d.edit(); !strings.Contains(diags, "extra") {
			t.Fatalf("the import of the missing extra is not reported:\n%s", diags)
		}
		d.write("extra", "pub fn more(): String {\n    \"more\"\n}\n", old)
		// The language server's answer to a created file: it analyzes the
		// open documents again, with no program kept.
		if read := d.reads(func() { d.dm.ReanalyzeOpen("") }); !slices.Contains(read, "extra") {
			t.Fatalf("after extra was created the analysis read %v", read)
		}
		if _, diags := d.edit(); strings.Contains(diags, "diag") {
			t.Fatalf("after extra was created the entry reports\n%s", diags)
		}
	})
}
