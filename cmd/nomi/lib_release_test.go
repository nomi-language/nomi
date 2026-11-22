package main

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"

	"github.com/nomi-language/nomi/internal/fileuri"
)

// libTestVersion is the version the file proxy serves the lib/ modules at.
const libTestVersion = "v0.0.0-20261104000000-abcdefabcdef"

// libTestModules are the lib/ modules the file proxy serves, by directory.
var libTestModules = []string{
	"lib/image", "lib/markdown", "lib/sql/migrate", "lib/sql/postgres", "lib/sql/sqlite", "lib/sql/sqlkit",
}

// TestLibRelease_AProjectOfLibModulesNeedsNoGo: a release `nomi`, with no Go
// on PATH, takes sqlite, sqlkit, migrate,
// markdown and image from a module proxy into the module cache, runs
// sqlkit's tool from there, and runs, tests, checks and builds the project.
// The lib/ modules are Nomi alone, so nothing is generated or built with Go.
//
// sqlkit's nomi.toml names its sibling lib/ modules by directory, which the
// module cache does not hold. A dependency's dir entries apply only in its
// own checkout, so the tool loads its modules as the project names them and
// writes the shop example's files as committed.
func TestLibRelease_AProjectOfLibModulesNeedsNoGo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the release environment is a Unix PATH without go (noGoPath)")
	}
	t.Parallel()
	b := releaseBuild(t)
	root := repoRoot(t)
	proxy := t.TempDir()
	var lock strings.Builder
	for _, dir := range libTestModules {
		m := module.Version{Path: "github.com/nomi-language/nomi/" + dir, Version: libTestVersion}
		hash, err := publishLibModule(filepath.Join(root, filepath.FromSlash(dir)), proxy, m)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&lock, "%s %s %s direct\n", m.Path, m.Version, hash)
	}
	proj := t.TempDir()
	libProject(t, proj, lock.String())
	cacheRoot := t.TempDir()
	env := releaseEnv(t, cacheRoot,
		"GOMODCACHE="+filepath.Join(t.TempDir(), "modcache"),
		"GOPROXY="+fileuri.FromPath(proxy),
		"GOSUMDB=off",
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

// publishLibModule writes the module in dir to the file proxy at proxy as
// m, and answers its zip's hash.
func publishLibModule(dir, proxy string, m module.Version) (string, error) {
	vdir := filepath.Join(proxy, filepath.FromSlash(m.Path), "@v")
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return "", err
	}
	zipPath := filepath.Join(vdir, m.Version+".zip")
	var buf bytes.Buffer
	if err := modzip.CreateFromDir(&buf, m, dir); err != nil {
		return "", fmt.Errorf("zipping %s: %w", dir, err)
	}
	goMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	for name, data := range map[string][]byte{
		"list":              []byte(m.Version + "\n"),
		m.Version + ".info": []byte(`{"Version":"` + m.Version + `","Time":"2026-11-04T00:00:00Z"}`),
		m.Version + ".mod":  goMod,
		m.Version + ".zip":  buf.Bytes(),
	} {
		if err := os.WriteFile(filepath.Join(vdir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	return dirhash.HashZip(zipPath, dirhash.Hash1)
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
		case "go.mod", "nomi.toml", "main.nomi", "README.md":
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
		fmt.Fprintf(&manifest, "%s = { module = \"github.com/nomi-language/nomi/%s\", version = %q }\n", path.Base(m), m, libTestVersion)
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
