//go:build darwin || linux

package stdio

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// accessTime is the access time Go's stat record holds.
func accessTime(info os.FileInfo) (time.Time, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(statAtime(st)), true
}

// linkCount is the link count Go's stat record holds.
func linkCount(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Nlink), true
}

// TestMetadata_AgreesWithGoStat holds every field `fs.metadata` answers on
// macOS and Linux to the stat record Go reads for the same file: the
// times, the link count, the device and inode, and the owner, who is the
// test's own user.
func TestMetadata_AgreesWithGoStat(t *testing.T) {
	for name, tc := range diskBackends(t) {
		t.Run(name, func(t *testing.T) {
			if e := writeAll(tc.b, tc.base+"f.txt", "four"); e != nil {
				t.Fatalf("write: %+v", e)
			}
			goInfo, err := os.Stat(filepath.Join(tc.os, "f.txt"))
			if err != nil {
				t.Fatal(err)
			}
			st := goInfo.Sys().(*syscall.Stat_t)
			info, e := tc.b.Stat(tc.base + "f.txt")
			if e != nil {
				t.Fatalf("metadata: %+v", e)
			}
			if !info.Modified.Equal(goInfo.ModTime()) || info.Size != 4 || info.Mode != 0o644&^int64(umask()) {
				t.Errorf("metadata %+v, Go %v %d %v", info, goInfo.ModTime(), goInfo.Size(), goInfo.Mode())
			}
			if !info.Accessed.Equal(time.Unix(statAtime(st))) || !info.Changed.Equal(time.Unix(statCtime(st))) {
				t.Errorf("accessed %v and changed %v, Go's %v and %v", info.Accessed, info.Changed, time.Unix(statAtime(st)), time.Unix(statCtime(st)))
			}
			if info.Links != uint64(st.Nlink) || info.ID == nil || info.ID.Inode != uint64(st.Ino) || info.ID.Device != statDevice(st) {
				t.Errorf("links %d and identity %+v, Go's %d, %d on %d", info.Links, info.ID, st.Nlink, st.Ino, statDevice(st))
			}
			if info.Owner == nil || info.Owner.User != uint64(os.Getuid()) || info.Owner.Group != uint64(st.Gid) {
				t.Errorf("owner %+v, want user %d group %d", info.Owner, os.Getuid(), st.Gid)
			}
		})
	}
}

// TestSetOwner_OwnUserOnlyUnlessRoot: giving a file the owner it has
// succeeds, through a path and inside a root; giving it to root's user is
// PermissionDenied for a user who is not root.
func TestSetOwner_OwnUserOnlyUnlessRoot(t *testing.T) {
	for name, tc := range diskBackends(t) {
		t.Run(name, func(t *testing.T) {
			p := tc.base + "o.txt"
			if e := writeAll(tc.b, p, ""); e != nil {
				t.Fatalf("write: %+v", e)
			}
			info, e := tc.b.Stat(p)
			if e != nil || info.Owner == nil {
				t.Fatalf("metadata: %+v, %+v", info, e)
			}
			if e := tc.b.Chown(p, info.Owner.User, info.Owner.Group, true); e != nil {
				t.Fatalf("set_owner to its own owner: %+v", e)
			}
			if e := tc.b.Symlink(tc.base+"l", "o.txt"); e != nil {
				t.Fatalf("symlink: %+v", e)
			}
			if e := tc.b.Chown(tc.base+"l", info.Owner.User, info.Owner.Group, false); e != nil {
				t.Fatalf("set_link_owner to its own owner: %+v", e)
			}
			if os.Getuid() == 0 {
				t.Skip("root may give a file any owner")
			}
			if e := tc.b.Chown(p, 0, 0, true); e == nil || e.Tag != rt.TagIOPermissionDenied || e.Path != p {
				t.Fatalf("set_owner to root answered %+v, want PermissionDenied", e)
			}
		})
	}
}

// TestOwnerName_NamesTheTestsUser: the test's own user has a name, and
// inside a root no user has one.
func TestOwnerName_NamesTheTestsUser(t *testing.T) {
	if _, found := OS.OwnerName(uint64(os.Getuid()), false); !found {
		t.Skip("this system's user database does not name the test's user")
	}
	inside, release, e := OS.Within(filepath.ToSlash(t.TempDir()))
	if e != nil {
		t.Fatalf("within: %+v", e)
	}
	defer release()
	if name, found := inside.OwnerName(uint64(os.Getuid()), false); found {
		t.Fatalf("inside a root the user is named %q", name)
	}
}

// umask is the process's file mode mask, read by setting it and setting it
// back.
func umask() int {
	m := syscall.Umask(0)
	syscall.Umask(m)
	return m
}
