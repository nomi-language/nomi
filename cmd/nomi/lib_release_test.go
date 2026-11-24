package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modfetch/modfetchtest"
)

// libTestVersion is the tag the test's host serves the first-party
// libraries' repository at.
const libTestVersion = "v0.9.0"

// libTestModules are the libraries the project names, by directory.
var libTestModules = []string{
	"lib/image", "lib/markdown", "lib/sql/migrate", "lib/sql/postgres", "lib/sql/sqlite", "lib/sql/sqlkit",
}

// TestLibRelease_AProjectOfLibModulesNeedsNoGo: a release `nomi`, with no Go
// and no git on PATH, takes sqlite, sqlkit, migrate, markdown and image
// from their repository's host into the module cache, runs sqlkit's tool
// from there, and runs, tests, checks and builds the project. The
// libraries are Nomi alone, so nothing is generated or built with Go.
//
// sqlkit's nomi.toml names its neighbor libraries by directory. In the
// module cache those are the same directories of the repository at the
// same tag, the modules the project names, so the tool loads its modules
// as the project names them and writes the shop example's files as
// committed.
func TestLibRelease_AProjectOfLibModulesNeedsNoGo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the release environment is a Unix PATH without go (noGoPath)")
	}
	t.Parallel()
	b := releaseBuild(t)
	root := repoRoot(t)
	host := modfetchtest.New(t)
	files := libRepoFiles(t, root, libTestModules)
	commit := host.Tag("github.com/nomi-language/nomi", libTestVersion, files)
	var lock strings.Builder
	for _, dir := range libTestModules {
		fmt.Fprintf(&lock, "github.com/nomi-language/nomi %s %s %s %s direct\n", dir, libTestVersion, commit, modfetchtest.Hash(files, dir))
	}
	proj := t.TempDir()
	libProject(t, proj, lock.String())
	cacheRoot := t.TempDir()
	env := releaseEnv(t, cacheRoot,
		"NOMI_MODULE_CACHE="+modfetchtest.CacheDir(t),
		"NOMI_TEST_MODULE_HOSTS="+host.Env("github.com"),
	)
	nomi := exe(filepath.Join(b.install, "nomi"))

	checkShopTool(t, env, proj, nomi)
	run := runProcess(t, env, proj, "", nomi, "run", "main.nomi")
	if run.exit != 0 || run.stdout != libProjectWant {
		t.Fatalf("nomi run exited %d:\nstdout: %q\nstderr: %s", run.exit, run.stdout, run.stderr)
	}
	test := runProcess(t, env, proj, "", nomi, "test", ".")
	if test.exit != 0 || !strings.Contains(test.stdout+test.stderr, "0 failed") {
		t.Fatalf("nomi test exited %d:\n%s%s", test.exit, test.stdout, test.stderr)
	}
	check := runProcess(t, env, proj, "", nomi, "check", "main.nomi")
	if check.exit != 0 {
		t.Fatalf("nomi check exited %d:\n%s%s", check.exit, check.stdout, check.stderr)
	}
	out := exe(filepath.Join(t.TempDir(), "shop"))
	build := runProcess(t, env, proj, "", nomi, "build", "main.nomi", "-o", out)
	if build.exit != 0 {
		t.Fatalf("nomi build exited %d:\n%s%s", build.exit, build.stdout, build.stderr)
	}
	built := runProcess(t, env, proj, "", out)
	if built.exit != 0 || built.stdout != libProjectWant {
		t.Fatalf("the built executable exited %d:\nstdout: %q\nstderr: %s", built.exit, built.stdout, built.stderr)
	}
	// Nothing was generated or built: the cache holds no wrapper or runner.
	entries, _ := os.ReadDir(cacheRoot)
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(cacheRoot, e.Name(), "main.go")); err == nil {
			t.Errorf("a command generated a wrapper in %s", e.Name())
		}
	}
}

// libRepoFiles is the files of the repository's directories dirs, by their
// slash paths from its root, as a tag of the repository holds them.
func libRepoFiles(t *testing.T, root string, dirs []string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			files[filepath.ToSlash(rel)] = string(data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// libProject writes a project at dir that names every lib/ module at
// libTestVersion, locked as lock says: the sqlkit shop example, with
// main.nomi also rendering Markdown and fitting an image, and a test of
// Markdown.
func libProject(t *testing.T, dir, lock string) {
	t.Helper()
	root := repoRoot(t)
	shop := filepath.Join(root, "lib", "sql", "examples", "shop")
	err := filepath.WalkDir(shop, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(shop, path)
		switch rel {
		case "nomi.toml", "main.nomi", "README.md":
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mustWrite(t, filepath.Join(dir, rel), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	png, err := os.ReadFile(filepath.Join(root, "lib", "image", "testdata", "gradient.png"))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "gradient.png"), string(png))
	var manifest strings.Builder
	manifest.WriteString("[module]\nname = \"shop\"\nentry_points = [\"main\"]\n\n[dependencies]\n")
	for _, m := range libTestModules {
		fmt.Fprintf(&manifest, "%s = { repo = \"github.com/nomi-language/nomi\", subdir = %q, version = %q }\n", path.Base(m), m, libTestVersion)
	}

	mustWrite(t, filepath.Join(dir, "nomi.toml"), manifest.String())
	mustWrite(t, filepath.Join(dir, "nomi.lock"), lock)
	mustWrite(t, filepath.Join(dir, "main.nomi"), libProjectMain)
	mustWrite(t, filepath.Join(dir, "media_test.nomi"), libProjectTest)
}

const libProjectMain = `import
    std::fs
    std::html.Html
    std::io
    std::sql.{self, Error}
    image.{self, Image}
    markdown
    queries
    schema
    sqlite
    sqlkit
end

fn main() -> Result<Unit, Error>
    io.print(Html.to_string(markdown.render("# Shop")))
    photo = image.decode(fs.read("gradient.png")!)!
    small = Image.fit(photo, 60, 60)
    io.print("logo ${Image.width(small)} by ${Image.height(small)}")

    db = sqlite.memory()?
    defer sql.close_quietly(db)

    sqlkit.migrate(db, schema.migrations)?
    user_id = queries.insert_user(db, "ada@example.com", Some("Ada"))?
    order_id = queries.insert_order(db, user_id, 12.50d)?
    changed = queries.set_status(db, order_id, .Paid)?
    io.print("order ${order_id} is paid (${changed.rows} row changed)")
    Ok(Unit)
end
`

const libProjectTest = `import
    std::html.Html
    markdown
end

test "markdown renders"
    assert Html.to_string(markdown.render("*hi*")) == "<p><em>hi</em></p>\n"
end
`

const libProjectWant = "<h1>Shop</h1>\n\nlogo 60 by 40\norder 1 is paid (1 row changed)\n"

// checkShopTool runs `nomi tool sqlkit generate` in the lib project at proj
// with its generated files removed, and checks that it writes them as the
// shop example commits them, and that `--check` then passes.
func checkShopTool(t *testing.T, env []string, proj, nomi string) {
	t.Helper()
	want := map[string]string{}
	for _, name := range []string{"queries.nomi", "schema.nomi", "schema_test.nomi"} {
		data, err := os.ReadFile(filepath.Join(proj, name))
		if err != nil {
			t.Fatal(err)
		}
		want[name] = string(data)
		if err := os.Remove(filepath.Join(proj, name)); err != nil {
			t.Fatal(err)
		}
	}
	gen := runProcess(t, env, proj, "", nomi, "tool", "sqlkit", "generate")
	if gen.exit != 0 {
		t.Fatalf("nomi tool sqlkit generate exited %d:\n%s%s", gen.exit, gen.stdout, gen.stderr)
	}
	for name, text := range want {
		got, err := os.ReadFile(filepath.Join(proj, name))
		if err != nil {
			t.Fatalf("the tool wrote no %s: %v", name, err)
		}
		if string(got) != text {
			t.Errorf("the tool wrote %s as\n%s\nwant the committed\n%s", name, got, text)
		}
	}
	check := runProcess(t, env, proj, "", nomi, "tool", "sqlkit", "generate", "--check")
	if check.exit != 0 {
		t.Fatalf("nomi tool sqlkit generate --check exited %d:\n%s%s", check.exit, check.stdout, check.stderr)
	}
}
