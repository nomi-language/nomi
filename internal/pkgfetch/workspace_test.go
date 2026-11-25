package pkgfetch

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
)

// kitFiles is example.com/kit's files: a package with no dependencies.
var kitFiles = map[string]string{
	"nomi.toml": "[package]\nname = \"kit\"\n",
	"kit.nomi":  "pub fn one() -> Int\n    1\nend\n",
}

// newWorkspace gives the test a package cache and a workspace of two
// members, billing and shop, whose [dependencies] are the given lines, and
// answers the workspace's root.
func newWorkspace(t *testing.T, billing, shop string) string {
	t.Helper()
	pkgfetchtest.Cache(t)
	root := filepath.Join(t.TempDir(), "ws")
	writeFile(t, filepath.Join(root, pkgcache.WorkspaceName), "# the workspace\nmembers = [\"billing\", \"shop\"]\n")
	for name, deps := range map[string]string{"billing": billing, "shop": shop} {
		writeFile(t, filepath.Join(root, name, "nomi.toml"), "[package]\nname = \""+name+"\"\n\n[dependencies]\n"+deps)
	}
	return root
}

func kitEntry(v string) string {
	return "kit = { repo = \"example.com/kit\", version = \"" + v + "\" }\n"
}

func noFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s exists", path)
	}
}

// `nomi fetch` in a member acts on the whole workspace: one selection for
// every member, written to the nomi.lock at the workspace's root, with
// `direct` for a package a member names, and no lock in the member. A
// plain fetch of the member then reads that lock.
func TestWorkspace_FetchInAMemberLocksTheWorkspace(t *testing.T) {
	s := serve(t)
	commits := map[string]string{}
	for _, v := range []string{"v1.2.0", "v1.3.0"} {
		commits[v] = s.Tag("example.com/kit", v, kitFiles)
	}
	root := newWorkspace(t, kitEntry("v1.2.0"), kitEntry("v1.3.0"))
	billing := filepath.Join(root, "billing")

	g, err := Fetch(billing, Options{Update: true})
	if err != nil {
		t.Fatal(err)
	}
	if !same(g.Root, root) {
		t.Errorf("the fetch's graph is rooted at %s, want the workspace's root %s", g.Root, root)
	}
	want := lockHeader() + "example.com/kit . v1.3.0 " + commits["v1.3.0"] + " " + pkgfetchtest.Hash(kitFiles, "") + " direct\n"
	if lock := readFile(t, filepath.Join(root, "nomi.lock")); lock != want {
		t.Errorf("the workspace's nomi.lock is\n%s\nwant\n%s", lock, want)
	}
	noFile(t, filepath.Join(billing, "nomi.lock"))

	g, err = Fetch(billing, Options{Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := g.Find(pkgcache.ID{Repo: "example.com/kit"}); !ok || d.Version != "v1.3.0" || !same(g.Root, billing) {
		t.Errorf("a plain fetch of billing takes kit as %+v in a graph rooted at %s; want v1.3.0 in billing's", d, g.Root)
	}
}

// `nomi add` in a member edits that member's nomi.toml and the
// workspace's nomi.lock, which keeps what the other member needs; `nomi
// remove` drops only what no member needs.
func TestWorkspace_AddAndRemoveInAMember(t *testing.T) {
	s := serve(t)
	s.Tag("example.com/kit", "v1.0.0", kitFiles)
	publishGreet(s, "v0.2.0")
	root := newWorkspace(t, kitEntry("v1.0.0"), "")
	billing, shop := filepath.Join(root, "billing"), filepath.Join(root, "shop")
	if _, err := Fetch(root, Options{Update: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := Add(shop, "", "example.com/greet", Options{}); err != nil {
		t.Fatal(err)
	}
	if manifest := readFile(t, filepath.Join(shop, "nomi.toml")); !strings.HasSuffix(manifest, "greet = { repo = \"example.com/greet\", version = \"v0.2.0\" }\n") {
		t.Errorf("shop's nomi.toml after add:\n%s", manifest)
	}
	if manifest := readFile(t, filepath.Join(billing, "nomi.toml")); strings.Contains(manifest, "greet") {
		t.Errorf("add in shop changed billing's nomi.toml:\n%s", manifest)
	}
	lock := readFile(t, filepath.Join(root, "nomi.lock"))
	for _, want := range []string{"example.com/greet . v0.2.0 ", "example.com/kit . v1.0.0 ", "example.com/strutil . v1.0.0 "} {
		if !strings.Contains(lock, want) {
			t.Errorf("the workspace's nomi.lock lacks %q:\n%s", want, lock)
		}
	}
	noFile(t, filepath.Join(shop, "nomi.lock"))

	removed, err := Remove(shop, "greet", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Unneeded) != 2 {
		t.Errorf("remove answered %v as unneeded, want greet and strutil", removed.Unneeded)
	}
	if lock := readFile(t, filepath.Join(root, "nomi.lock")); !strings.Contains(lock, "example.com/kit . v1.0.0 ") || strings.Contains(lock, "greet") {
		t.Errorf("the workspace's nomi.lock after remove:\n%s", lock)
	}
}

// `nomi update` in a member raises the repositories only a dependency
// names in the workspace's nomi.lock, and writes neither
// nomi-workspace.toml nor a member's nomi.toml for them; every member's
// build reads the lock.
func TestWorkspace_UpdateRaisesInTheWorkspaceLock(t *testing.T) {
	publishThroughGreet(t)
	root := newWorkspace(t, "greet = { repo = \"example.com/greet\", version = \"v0.1.0\" }\n", "")
	billing := filepath.Join(root, "billing")
	if _, err := Fetch(billing, Options{Update: true}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range []string{pkgcache.WorkspaceName, "billing/nomi.toml", "shop/nomi.toml"} {
		files[f] = readFile(t, filepath.Join(root, f))
	}
	updated, err := Update(filepath.Join(root, "shop"), "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"example.com/strutil", "example.com/words"}; !reflect.DeepEqual(updated.Raised, want) {
		t.Fatalf("Update raised %v, want %v", updated.Raised, want)
	}
	for f, was := range files {
		if text := readFile(t, filepath.Join(root, f)); text != was {
			t.Errorf("%s after update:\n%s\nwant it unchanged:\n%s", f, text, was)
		}
	}
	if v, _ := lockedAt(t, root, "example.com/strutil"); v != "v1.1.0" {
		t.Errorf("the workspace's nomi.lock takes strutil at %q, want v1.1.0", v)
	}
	g, err := Fetch(billing, Options{Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := g.Find(pkgcache.ID{Repo: "example.com/strutil"}); d.Version != "v1.1.0" {
		t.Errorf("billing's build takes strutil at %s, want the lock's v1.1.0", d.Version)
	}

	behind, err := Outdated(billing, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(behind) != 1 || behind[0].Repo != "example.com/strutil" || behind[0].Newest != "v2.0.0" {
		t.Errorf("Outdated answered %+v, want strutil behind v2.0.0", behind)
	}
}

// `nomi outdated` in a member covers every member's packages.
func TestWorkspace_OutdatedCoversEveryMember(t *testing.T) {
	s := serve(t)
	for _, v := range []string{"v1.0.0", "v1.1.0"} {
		s.Tag("example.com/kit", v, kitFiles)
	}
	root := newWorkspace(t, "", kitEntry("v1.0.0"))
	if _, err := Fetch(root, Options{Update: true}); err != nil {
		t.Fatal(err)
	}
	behind, err := Outdated(filepath.Join(root, "billing"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Behind{{Repo: "example.com/kit", Version: "v1.0.0", Line: "v1.1.0", Newest: "v1.1.0"}}
	if !reflect.DeepEqual(behind, want) {
		t.Errorf("Outdated in billing answered %+v, want shop's kit: %+v", behind, want)
	}
}

// A member's own nomi.lock is refused, saying to delete it.
func TestWorkspace_AMembersOwnLockIsRefused(t *testing.T) {
	serve(t)
	root := newWorkspace(t, "", "")
	stray := filepath.Join(root, "shop", "nomi.lock")
	writeFile(t, stray, lockHeader())
	_, err := Fetch(filepath.Join(root, "billing"), Options{Update: true})
	want := stray + ": shop is a member of the workspace at " + root + ", whose nomi.lock locks every member's packages; delete this nomi.lock"
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("a member's own lock: %v\nwant %s...", err, want)
	}
	noFile(t, filepath.Join(root, "nomi.lock"))
}

// A member another project fetches by its repository and subdir is read
// alone: the workspace's file and lock, published beside it, are not
// read, so the lock they hold changes nothing.
func TestWorkspace_AMemberFetchedByAConsumerIsReadAlone(t *testing.T) {
	s := serve(t)
	s.Tag("example.com/strutil", "v1.0.0", strutilFiles)
	s.Tag("example.com/strutil", "v1.1.0", strutilFiles)
	s.Tag("example.com/mono", "v1.0.0", map[string]string{
		pkgcache.WorkspaceName: "members = [\"billing\"]\n",
		"nomi.lock":            "example.com/strutil . v1.1.0 " + strings.Repeat("a", 40) + " sha256:" + strings.Repeat("b", 64) + "\n",
		"billing/nomi.toml":    "[package]\nname = \"billing\"\n\n[dependencies]\nstrutil = { repo = \"example.com/strutil\", version = \"v1.0.0\" }\n",
		"billing/billing.nomi": "pub fn total() -> Int\n    1\nend\n",
	})
	app, _ := setup(t)
	added, err := Add(app, "", "example.com/mono/billing@v1.0.0", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if added.Name != "billing" || added.Package.Subdir != "billing" {
		t.Errorf("Add answered %+v", added)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/strutil . v1.0.0 ") {
		t.Errorf("the consumer's nomi.lock takes strutil other than at billing's v1.0.0:\n%s", lock)
	}
}
