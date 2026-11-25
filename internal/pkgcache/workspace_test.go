package pkgcache

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// kitFiles is example.com/kit at a version: a package that requires
// example.com/util v1.0.0.
var kitFiles = map[string]string{
	ManifestName: "[package]\nname = \"kit\"\n\n[dependencies]\nutil = { repo = \"example.com/util\", version = \"v1.0.0\" }\n",
}

var utilFiles = map[string]string{ManifestName: "[package]\nname = \"util\"\n"}

// workspace writes a workspace at a new directory: its nomi-workspace.toml
// with text ws, and a member package at each key of members whose
// nomi.toml's [dependencies] holds that key's lines. It answers the root.
func workspace(t *testing.T, ws string, members map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "ws")
	writeFile(t, filepath.Join(root, WorkspaceName), ws)
	for dir, deps := range members {
		writeFile(t, filepath.Join(root, dir, ManifestName), "[package]\nname = \""+filepath.Base(dir)+"\"\n\n[dependencies]\n"+deps)
	}
	return root
}

func kitAt(v string) string {
	return "kit = { repo = \"example.com/kit\", version = \"" + v + "\" }\n"
}

func versionsOf(g *Graph) map[string]string {
	out := map[string]string{}
	for _, d := range g.Deps {
		out[d.ID.String()] = d.Version
	}
	return out
}

// Two members that need one repository at two versions of one line take
// the higher, together: the workspace's resolve has every member's
// requirements as its roots, marks a package direct when any member names
// it, and a member's own walk reads the lock at the workspace's root,
// which holds a repository only a dependency names above what it asks for.
// A resolve starts from the versions it is given and from nothing else.
func TestWorkspace_MembersShareOneSelection(t *testing.T) {
	cache := useCache(t)
	for _, v := range []string{"v1.2.0", "v1.3.0"} {
		cached(t, cache, "example.com/kit", v, kitFiles)
	}
	for _, v := range []string{"v1.0.0", "v1.1.0"} {
		cached(t, cache, "example.com/util", v, utilFiles)
	}
	root := workspace(t, "members = [\"billing\", \"shop\"]\n",
		map[string]string{"billing": kitAt("v1.2.0"), "shop": kitAt("v1.3.0")})

	g, err := Resolve(filepath.Join(root, "billing"), cacheOnly, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"example.com/kit": "v1.3.0", "example.com/util": "v1.0.0"}; !reflect.DeepEqual(versionsOf(g), want) {
		t.Errorf("the workspace resolves to %v, want %v", versionsOf(g), want)
	}
	raised := map[string]string{"example.com/util": "v1.1.0", "example.com/unused": "v1.0.0"}
	if g, err := Resolve(root, cacheOnly, raised); err != nil {
		t.Fatal(err)
	} else if want := map[string]string{"example.com/kit": "v1.3.0", "example.com/util": "v1.1.0"}; !reflect.DeepEqual(versionsOf(g), want) {
		t.Errorf("the workspace resolved from %v is %v, want %v", raised, versionsOf(g), want)
	}
	if !sameDir(g.Root, root) {
		t.Errorf("the workspace's graph is rooted at %s, want %s", g.Root, root)
	}
	for _, d := range g.Deps {
		if d.Direct != (d.Repo == "example.com/kit") {
			t.Errorf("%s: direct is %v", d.ID, d.Direct)
		}
	}

	// billing alone asks for v1.2.0; the workspace's lock holds v1.3.0, and
	// util at v1.1.0, above the v1.0.0 kit asks for.
	writeFile(t, filepath.Join(root, LockName), lockHeader+
		"example.com/kit . v1.3.0 "+strings.Repeat("a", 40)+" sha256:"+strings.Repeat("b", 64)+" direct\n"+
		"example.com/util . v1.1.0 "+strings.Repeat("c", 40)+" sha256:"+strings.Repeat("d", 64)+"\n")
	g, err = Walk(filepath.Join(root, "billing"), cacheOnly)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"example.com/kit": "v1.3.0", "example.com/util": "v1.1.0"}; !reflect.DeepEqual(versionsOf(g), want) {
		t.Errorf("billing's walk is %v, want %v", versionsOf(g), want)
	}
	if !sameDir(g.Root, filepath.Join(root, "billing")) || g.Names["kit"] == "" {
		t.Errorf("billing's walk is rooted at %s with names %v; a member is its own project for names", g.Root, g.Names)
	}
}

// Two members whose requirements on one repository one build cannot take
// together are an error that names both members and both entries.
func TestWorkspace_MembersInTwoLinesFail(t *testing.T) {
	cache := useCache(t)
	for _, v := range []string{"v1.2.0", "v2.0.0"} {
		cached(t, cache, "example.com/kit", v, kitFiles)
	}
	cached(t, cache, "example.com/util", "v1.0.0", utilFiles)
	root := workspace(t, "members = [\"billing\", \"shop\"]\n",
		map[string]string{"billing": kitAt("v1.2.0"), "shop": "kit2 = { repo = \"example.com/kit\", version = \"v2.0.0\" }\n"})
	_, err := Resolve(root, cacheOnly, nil)
	want := "workspace members billing and shop cannot share one build: billing ([dependencies] kit) requires example.com/kit v1.2.0 and " +
		"shop ([dependencies] kit2) requires example.com/kit v2.0.0: one tag versions a whole repository"
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("two members in two lines: %v\nwant %s...", err, want)
	}
}

// The workspace's nomi.lock is held to every member's requirements: one in
// another line than a member's entry is an error naming that member and
// entry, in the whole workspace's walk and in that member's own.
func TestWorkspace_ALockInAnotherLineThanAMemberFails(t *testing.T) {
	cache := useCache(t)
	for _, v := range []string{"v1.2.0", "v2.0.0"} {
		cached(t, cache, "example.com/kit", v, kitFiles)
	}
	cached(t, cache, "example.com/util", "v1.0.0", utilFiles)
	root := workspace(t, "members = [\"billing\", \"shop\"]\n",
		map[string]string{"billing": "", "shop": kitAt("v1.2.0")})
	writeFile(t, filepath.Join(root, LockName), lockHeader+lockLine("example.com/kit", "v2.0.0"))
	want := "nomi.lock holds example.com/kit at v2.0.0 and shop ([dependencies] kit) requires example.com/kit v1.2.0: one tag versions a whole repository"
	for what, walk := range map[string]func() (*Graph, error){
		"WalkAll":     func() (*Graph, error) { return WalkAll(root, cacheOnly) },
		"shop's Walk": func() (*Graph, error) { return Walk(filepath.Join(root, "shop"), cacheOnly) },
	} {
		_, err := walk()
		var clash *ClashError
		if !errors.As(err, &clash) || !clash.Held || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s with the lock in another line than shop: %v\nwant %s...", what, err, want)
		}
	}
	if _, err := Walk(filepath.Join(root, "billing"), cacheOnly); err != nil {
		t.Errorf("billing, which names no kit, walks: %v", err)
	}
}

// One member taking a package from a directory and another fetching it is
// an error naming both: a workspace takes each package from one place.
func TestWorkspace_APackageFromTwoPlacesFails(t *testing.T) {
	cache := useCache(t)
	cached(t, cache, "example.com/kit", "v1.2.0", kitFiles)
	cached(t, cache, "example.com/util", "v1.0.0", utilFiles)
	root := workspace(t, "members = [\"billing\", \"shop\"]\n", map[string]string{
		"billing": "kit = { repo = \"example.com/kit\", version = \"v1.2.0\", dir = \"../../kit\" }\n",
		"shop":    kitAt("v1.2.0"),
	})
	writeFile(t, filepath.Join(filepath.Dir(root), "kit", ManifestName), "[package]\nname = \"kit\"\n")
	_, err := Resolve(root, cacheOnly, nil)
	want := "workspace members billing and shop take example.com/kit from two places: billing from the directory ../kit, and shop fetched at v1.2.0; a workspace takes each package from one place"
	if err == nil || err.Error() != want {
		t.Fatalf("a package from a directory and from its repository: %v\nwant %s", err, want)
	}
}

// A member is named by an entry: a glob takes each directory it matches
// that holds a nomi.toml, sorted. A directory with no nomi.toml under the
// root is no package and in no workspace.
func TestWorkspaceOf_EntriesNameTheMembers(t *testing.T) {
	root := workspace(t, "members = [\"lib/*\", \"app\"]\n", map[string]string{"app": "", "lib/b": "", "lib/a": ""})
	if err := os.MkdirAll(filepath.Join(root, "lib", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws, err := WorkspaceOf(filepath.Join(root, "lib", "b"))
	if err != nil {
		t.Fatal(err)
	}
	var members []string
	for _, m := range ws.Members {
		members = append(members, filepath.ToSlash(relTo(root, m)))
	}
	if want := []string{"app", "lib/a", "lib/b"}; !reflect.DeepEqual(members, want) {
		t.Errorf("the members are %v, want %v", members, want)
	}
	if ws, err := WorkspaceOf(filepath.Join(root, "lib", "notes")); err != nil || ws != nil {
		t.Errorf("a directory with no nomi.toml: WorkspaceOf answered %v, %v; want none", ws, err)
	}
	if ws, err := WorkspaceOf(root); err != nil || ws == nil || !sameDir(ws.Root, root) {
		t.Errorf("the workspace's root: WorkspaceOf answered %v, %v; want its own", ws, err)
	}
}

// Each problem in a workspace is refused, naming the file: with no
// reader installed (the analysis package installs one), a problem names
// its file and not its line.
func TestWorkspaceOf_Refuses(t *testing.T) {
	for _, c := range []struct {
		name, ws string
		setup    func(root string)
		want     string
	}{
		{"no members", "exclude = [\"vendor\"]\n", nil, "a workspace needs members"},
		{"empty members", "members = []\n", nil, "members lists no package"},
		{"an entry above the root", "members = [\"../billing\"]\n", nil, "members: \"../billing\" is not a directory below the workspace's root"},
		{"a pattern character", "members = [\"lib/?\"]\n", nil, "members: \"lib/?\" holds a pattern character other than `*`"},
		{"an entry twice", "members = [\"billing\", \"billing\"]\n", nil, "members: lists billing twice"},
		{"a literal entry with no nomi.toml", "members = [\"billing\", \"shop\"]\n", nil, "members: shop holds no nomi.toml; a member is a package's directory"},
		{"a member with no package", "members = [\"billing\", \"lib/*\"]\n",
			func(root string) { writeFile(t, filepath.Join(root, "lib", "x", ManifestName), "[dependencies]\n") },
			"members: lib/x's nomi.toml has no [package] name; a member is a package"},
		{"a nomi.toml beside it", "members = [\"billing\"]\n",
			func(root string) { writeFile(t, filepath.Join(root, ManifestName), "[package]\nname = \"root\"\n") },
			"this directory holds a nomi.toml too: a workspace's root is not a package"},
		{"a member's own workspace", "members = [\"billing\", \"shop\"]\n",
			func(root string) {
				writeFile(t, filepath.Join(root, "shop", ManifestName), "[package]\nname = \"shop\"\n")
				writeFile(t, filepath.Join(root, "shop", WorkspaceName), "members = [\"x\"]\n")
			},
			"members: shop holds a " + WorkspaceName + " of its own"},
		{"an entry across segments", "members = [\"lib/**\"]\n", nil, "members: \"lib/**\" holds `**`"},
		{"an exclude entry twice", "members = [\"billing\"]\nexclude = [\"x/*\", \"x/*\"]\n", nil, "exclude: lists x/* twice"},
		{"a literal exclude entry with no directory", "members = [\"billing\"]\nexclude = [\"gone\"]\n", nil, "exclude: gone is no directory"},
		{"a member excluded", "members = [\"billing\"]\nexclude = [\"bill*\"]\n", nil,
			"exclude: bill* takes in billing, which members names; a package is a member or excluded, not both"},
		{"a member below an excluded directory", "members = [\"lib/a\"]\nexclude = [\"lib\"]\n",
			func(root string) {
				writeFile(t, filepath.Join(root, "lib", "a", ManifestName), "[package]\nname = \"a\"\n")
			},
			"exclude: lib takes in lib/a, which members names"},
		{"a member's own lock", "members = [\"billing\"]\n",
			func(root string) { writeFile(t, filepath.Join(root, "billing", LockName), lockHeader) },
			"billing is a member of the workspace at "},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := workspace(t, c.ws, map[string]string{"billing": ""})
			if c.setup != nil {
				c.setup(root)
			}
			_, err := WorkspaceOf(filepath.Join(root, "billing"))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("WorkspaceOf: %v\nwant it to say %q", err, c.want)
			}
		})
	}
}

// The lookup walks up from the package's root through every parent and
// the nearest nomi-workspace.toml decides: one three levels up whose
// entry names the package governs it.
func TestWorkspaceOf_ThreeLevelsUpGoverns(t *testing.T) {
	root := workspace(t, "members = [\"a/b/c\"]\n", map[string]string{"a/b/c": ""})
	ws, err := WorkspaceOf(filepath.Join(root, "a", "b", "c"))
	if err != nil || ws == nil || !sameDir(ws.Root, root) {
		t.Fatalf("a workspace three levels up: WorkspaceOf answered %v, %v; want the one at %s", ws, err, root)
	}
}

// A package whose nearest nomi-workspace.toml does not name it is an
// error, though one further up names it, since that one is not read, and
// at any depth, inside a member's directory too.
func TestWorkspaceOf_ThePackageTheNearestFileDoesNotNameIsAnError(t *testing.T) {
	root := workspace(t, "members = [\"inner/app\"]\n", map[string]string{"inner/app": "", "inner/other": ""})
	inner := filepath.Join(root, "inner")
	writeFile(t, filepath.Join(inner, WorkspaceName), "members = [\"other\"]\n")
	fixture := filepath.Join(inner, "other", "testdata", "fixture")
	writeFile(t, filepath.Join(fixture, ManifestName), "[package]\nname = \"fixture\"\n")
	for _, c := range []struct{ dir, entry string }{
		{filepath.Join(inner, "app"), "app"},
		{fixture, "other/testdata/fixture"},
	} {
		_, err := WorkspaceOf(c.dir)
		want := filepath.Join(c.dir, ManifestName) + ":1: this package is under the workspace " + filepath.Join(inner, WorkspaceName) +
			", which does not list it; add \"" + c.entry + "\" to its members, or to its exclude if it keeps a nomi.lock of its own"
		var unlisted *UnlistedError
		if !errors.As(err, &unlisted) || err.Error() != want {
			t.Errorf("WorkspaceOf(%s): %v\nwant %s", c.dir, err, want)
		}
	}
}

// With no nomi-workspace.toml anywhere above it, a package is on its own;
// and a nomi-workspace.toml the walk cannot read ends it there, with the
// package on its own.
func TestWorkspaceOf_NoFileOrAnUnreadableOneIsOnItsOwn(t *testing.T) {
	app := filepath.Join(t.TempDir(), "app")
	writeFile(t, filepath.Join(app, ManifestName), "[package]\nname = \"app\"\n")
	if ws, err := WorkspaceOf(app); err != nil || ws != nil {
		t.Fatalf("no workspace file anywhere: WorkspaceOf answered %v, %v; want none", ws, err)
	}
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("a file's mode does not stop this process reading it")
	}
	root := workspace(t, "members = [\"billing\"]\n", map[string]string{"billing": ""})
	file := filepath.Join(root, WorkspaceName)
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o644) })
	if ws, err := WorkspaceOf(filepath.Join(root, "billing")); err != nil || ws != nil {
		t.Fatalf("an unreadable workspace file: WorkspaceOf answered %v, %v; want none", ws, err)
	}
}

// A package exclude names, or one below a directory it names, is on its
// own: in no workspace, with its own lock, its own selection, which may
// take a repository in another line than the workspace does, and outside
// the workspace's. A pattern that matches nothing is no error. A member may
// still name an excluded package by its directory, whose own requirements
// then join the member's selection as any directory package's do.
func TestWorkspace_AnExcludedPackageIsOnItsOwn(t *testing.T) {
	cache := useCache(t)
	for _, v := range []string{"v1.0.0", "v2.0.0"} {
		cached(t, cache, "example.com/util", v, utilFiles)
	}
	root := workspace(t, "members = [\"billing\"]\nexclude = [\"vendor/*\", \"templates\", \"none/*\"]\n", map[string]string{
		"billing":                "util = { repo = \"example.com/util\", version = \"v1.0.0\" }\n",
		"vendor/old":             "util = { repo = \"example.com/util\", version = \"v2.0.0\" }\n",
		"templates/app/scaffold": "",
	})
	old := filepath.Join(root, "vendor", "old")
	writeFile(t, filepath.Join(old, LockName), lockHeader)
	for _, dir := range []string{old, filepath.Join(root, "templates", "app", "scaffold")} {
		if ws, err := WorkspaceOf(dir); err != nil || ws != nil {
			t.Errorf("WorkspaceOf(%s) answered %v, %v; want none", dir, ws, err)
		}
	}
	g, err := Resolve(root, cacheOnly, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"example.com/util": "v1.0.0"}; !reflect.DeepEqual(versionsOf(g), want) {
		t.Errorf("the workspace resolves to %v, want %v", versionsOf(g), want)
	}
	if g, err := Resolve(old, cacheOnly, nil); err != nil || !reflect.DeepEqual(versionsOf(g), map[string]string{"example.com/util": "v2.0.0"}) || !sameDir(g.Root, old) {
		t.Errorf("the excluded package resolves to %v, %v; want its own graph at util v2.0.0", g, err)
	}

	writeFile(t, filepath.Join(root, "vendor", "kit", ManifestName), "[package]\nname = \"kit\"\n\n[dependencies]\n"+
		"util = { repo = \"example.com/util\", version = \"v1.0.0\" }\n")
	writeFile(t, filepath.Join(root, "shop", ManifestName), "[package]\nname = \"shop\"\n\n[dependencies]\nkit = { dir = \"../vendor/kit\" }\n")
	writeFile(t, filepath.Join(root, WorkspaceName), "members = [\"billing\", \"shop\"]\nexclude = [\"vendor/*\", \"templates\"]\n")
	g, err = Resolve(root, cacheOnly, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := g.Named("kit"); !ok || !d.Local {
		t.Errorf("shop's directory dependency on an excluded package is not in the workspace's graph: %+v", g.Deps)
	}
	if want := map[string]string{"": "", "example.com/util": "v1.0.0"}; !reflect.DeepEqual(versionsOf(g), want) {
		t.Errorf("the workspace resolves to %v, want %v", versionsOf(g), want)
	}
}
