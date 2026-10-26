package stdio

import (
	"reflect"
	"testing"
)

// TestMemoryReadIsContentsOnly holds Memory.Read to what the tour's
// doctest counts as reading a file: a successful open of its contents, by
// stream or at a position, once each and through a symlink to its target.
// A listing, metadata, a missing file and a refused escape are not reads.
func TestMemoryReadIsContentsOnly(t *testing.T) {
	m := NewMemory("/work")
	for _, p := range []string{"a.txt", "b.txt", "listed.txt", "site/in.txt", "secret.txt"} {
		m.Put(p, []byte(p))
	}
	if e := m.Symlink("link.txt", "b.txt"); e != nil {
		t.Fatalf("symlink: %+v", e)
	}
	if _, e := m.List("."); e != nil {
		t.Fatalf("list: %+v", e)
	}
	if _, e := m.Stat("listed.txt"); e != nil {
		t.Fatalf("stat: %+v", e)
	}
	if _, e := m.Open("missing.txt"); e == nil {
		t.Fatal("open of a missing file succeeded")
	}
	for _, p := range []string{"a.txt", "a.txt", "link.txt"} {
		if _, e := m.Open(p); e != nil {
			t.Fatalf("open %s: %+v", p, e)
		}
	}
	inside, release, e := m.Within("site")
	if e != nil {
		t.Fatalf("within: %+v", e)
	}
	defer release()
	if _, e := inside.Open("../secret.txt"); e == nil {
		t.Fatal("within opened a file outside it")
	}
	f, e := inside.OpenFile("in.txt", AccessRead)
	if e != nil {
		t.Fatalf("open_file: %+v", e)
	}
	if want := []string{"/work/a.txt", "/work/b.txt"}; !reflect.DeepEqual(m.Read(), want) {
		t.Fatalf("before a read at a position, Read is %q, want %q", m.Read(), want)
	}
	if _, err := f.F.ReadAt(make([]byte, 2), 0); err != nil {
		t.Fatalf("read_at: %v", err)
	}
	if want := []string{"/work/a.txt", "/work/b.txt", "/work/site/in.txt"}; !reflect.DeepEqual(m.Read(), want) {
		t.Fatalf("Read is %q, want %q", m.Read(), want)
	}
}
