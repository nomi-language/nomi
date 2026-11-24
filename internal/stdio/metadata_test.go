//go:build !(js && wasm)

package stdio

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// diskBackends is the disk as a path-taking call reaches it and as a `Dir`
// reaches it (an *os.Root), each with the directory a test works in and
// the path it names that directory by.
func diskBackends(t *testing.T) map[string]struct {
	b    Backend
	dir  string
	os   string
	base string
} {
	t.Helper()
	whole := t.TempDir()
	rooted := t.TempDir()
	inside, release, e := OS.Within(filepath.ToSlash(rooted))
	if e != nil {
		t.Fatalf("within: %+v", e)
	}
	t.Cleanup(release)
	return map[string]struct {
		b    Backend
		dir  string
		os   string
		base string
	}{
		"disk":   {OS, filepath.ToSlash(whole), whole, filepath.ToSlash(whole) + "/"},
		"within": {inside, ".", rooted, ""},
	}
}

// TestSetTimes_ReadBackAsGoReadsThem holds `fs.set_times` and the times
// `fs.metadata` answers to Go's own stat: the times set are the ones Go
// reads back, to the granularity the file system keeps, and metadata
// answers exactly what Go reads. A nil time is left as it was.
func TestSetTimes_ReadBackAsGoReadsThem(t *testing.T) {
	for name, tc := range diskBackends(t) {
		t.Run(name, func(t *testing.T) {
			p := tc.base + "f.txt"
			if e := writeAll(tc.b, p, "f"); e != nil {
				t.Fatalf("write: %+v", e)
			}
			modified := time.Date(2001, 9, 9, 1, 46, 40, 123456789, time.UTC)
			accessed := time.Date(2004, 11, 12, 3, 4, 5, 987654321, time.UTC)
			if e := tc.b.Chtimes(p, &modified, &accessed); e != nil {
				t.Fatalf("set_times: %+v", e)
			}
			goInfo, err := os.Stat(filepath.Join(tc.os, "f.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if d := goInfo.ModTime().Sub(modified); d < -2*time.Second || d > 2*time.Second {
				t.Fatalf("Go reads the modification time as %v, set %v", goInfo.ModTime(), modified)
			}
			info, e := tc.b.Stat(p)
			if e != nil {
				t.Fatalf("metadata: %+v", e)
			}
			if !info.Modified.Equal(goInfo.ModTime()) {
				t.Errorf("metadata's modified is %v, Go's %v", info.Modified, goInfo.ModTime())
			}
			if goAccessed, ok := accessTime(goInfo); ok {
				if !info.Accessed.Equal(goAccessed) {
					t.Errorf("metadata's accessed is %v, Go's %v", info.Accessed, goAccessed)
				}
				if d := goAccessed.Sub(accessed); d < -2*time.Second || d > 2*time.Second {
					t.Errorf("Go reads the access time as %v, set %v", goAccessed, accessed)
				}
			}
			later := modified.Add(time.Hour)
			if e := tc.b.Chtimes(p, &later, nil); e != nil {
				t.Fatalf("set_times of the modification time alone: %+v", e)
			}
			again, e := tc.b.Stat(p)
			if e != nil {
				t.Fatalf("metadata: %+v", e)
			}
			if !again.Accessed.Equal(info.Accessed) || !again.Modified.After(info.Modified) {
				t.Errorf("after setting the modification time alone, metadata is %+v, was %+v", again, info)
			}
		})
	}
}

// TestCreateLink_IsOneFileAsGoSeesIt holds `fs.create_link` to Go's view of
// the two names: one file (os.SameFile), counted twice, with the one
// identity metadata answers for both; a copy is another file.
func TestCreateLink_IsOneFileAsGoSeesIt(t *testing.T) {
	for name, tc := range diskBackends(t) {
		t.Run(name, func(t *testing.T) {
			a, b, c := tc.base+"a.txt", tc.base+"b.txt", tc.base+"c.txt"
			if e := writeAll(tc.b, a, "a"); e != nil {
				t.Fatalf("write: %+v", e)
			}
			if e := tc.b.Link(b, a); e != nil {
				t.Fatalf("create_link: %+v", e)
			}
			if e := writeAll(tc.b, c, "a"); e != nil {
				t.Fatalf("write the copy: %+v", e)
			}
			stat := func(name string) os.FileInfo {
				info, err := os.Stat(filepath.Join(tc.os, name))
				if err != nil {
					t.Fatal(err)
				}
				return info
			}
			if !os.SameFile(stat("a.txt"), stat("b.txt")) || os.SameFile(stat("a.txt"), stat("c.txt")) {
				t.Fatal("Go does not see a.txt and b.txt as one file and c.txt as another")
			}
			infoA, e := tc.b.Stat(a)
			if e != nil {
				t.Fatalf("metadata: %+v", e)
			}
			infoB, _ := tc.b.Stat(b)
			infoC, _ := tc.b.Stat(c)
			if infoA.Links != 2 || infoB.Links != 2 || infoC.Links != 1 {
				t.Errorf("links: a %d, b %d, c %d; want 2, 2, 1", infoA.Links, infoB.Links, infoC.Links)
			}
			if infoA.ID == nil || infoB.ID == nil || infoC.ID == nil || *infoA.ID != *infoB.ID || *infoA.ID == *infoC.ID {
				t.Errorf("identities: a %+v, b %+v, c %+v", infoA.ID, infoB.ID, infoC.ID)
			}
			if goLinks, ok := linkCount(stat("a.txt")); ok && goLinks != infoA.Links {
				t.Errorf("metadata counts %d links, Go %d", infoA.Links, goLinks)
			}
			if e := tc.b.Link(tc.base+"d.txt", tc.base+"none"); e == nil || e.Tag != rt.TagIONotFound || e.Path != tc.base+"none" {
				t.Errorf("a link to nothing answered %+v", e)
			}
		})
	}
}

// TestCreateLink_LinksASymlinkAsItself holds the disk to one rule on every
// system: a symlink given a second name is linked itself, as an *os.Root
// links it and Linux's link(2) does, where macOS's link(2) would follow it.
func TestCreateLink_LinksASymlinkAsItself(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making a symlink on Windows needs a privilege a test may not hold")
	}
	for name, tc := range diskBackends(t) {
		t.Run(name, func(t *testing.T) {
			if e := writeAll(tc.b, tc.base+"t.txt", "t"); e != nil {
				t.Fatalf("write: %+v", e)
			}
			if e := tc.b.Symlink(tc.base+"s", "t.txt"); e != nil {
				t.Fatalf("symlink: %+v", e)
			}
			if e := tc.b.Link(tc.base+"h", tc.base+"s"); e != nil {
				t.Fatalf("create_link: %+v", e)
			}
			info, err := os.Lstat(filepath.Join(tc.os, "h"))
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("the second name is %v, %v; want a symlink", info, err)
			}
		})
	}
}

// TestDiskUsage_SizesInOrder: the disk's sizes are positive and ordered, on
// the whole disk and inside a root, where the path is opened under the
// root rather than joined again.
func TestDiskUsage_SizesInOrder(t *testing.T) {
	for name, tc := range diskBackends(t) {
		t.Run(name, func(t *testing.T) {
			u, e := tc.b.DiskUsage(tc.dir)
			if e != nil {
				t.Fatalf("disk_usage: %+v", e)
			}
			if u.Total == 0 || u.Free > u.Total || u.Available > u.Free {
				t.Fatalf("disk_usage answered %+v", u)
			}
		})
	}
	inside, release, e := OS.Within(filepath.ToSlash(t.TempDir()))
	if e != nil {
		t.Fatalf("within: %+v", e)
	}
	defer release()
	if _, e := inside.DiskUsage(".."); e == nil || e.Tag != rt.TagIOInvalidPath {
		t.Fatalf("disk_usage of the root's parent answered %+v, want InvalidPath", e)
	}
}

// TestMetadata_ModeKeepsTheSpecialBits: a sticky directory's mode carries
// 0o1000 beside its permission bits, as Unix numbers it.
func TestMetadata_ModeKeepsTheSpecialBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no sticky bit")
	}
	dir := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, os.ModeSticky|0o755); err != nil {
		t.Fatal(err)
	}
	info, e := OS.Stat(filepath.ToSlash(dir))
	if e != nil {
		t.Fatalf("metadata: %+v", e)
	}
	if info.Mode != 0o1755 {
		t.Fatalf("mode is %o, want 1755", info.Mode)
	}
	if FileMode(info.Mode) != os.ModeSticky|0o755 {
		t.Fatalf("FileMode(%o) is %v", info.Mode, FileMode(info.Mode))
	}
}
