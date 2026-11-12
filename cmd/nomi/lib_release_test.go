package main

// A release `nomi` with the lib archive's nomi-lib and nomi-vm-lib runs,
// tests and builds a project of the first-party lib/ modules with no Go
// toolchain, and a project the prebuilt binaries cannot serve says what
// needs Go. See internal/ffirun/libprebuilt.go.

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"

	"github.com/nomi-language/nomi/internal/fileuri"
)

// libTestVersion is the lib/ modules' version the fake release carries, and
// libOtherVersion another commit's.
const (
	libTestVersion  = "v0.0.0-20261104000000-abcdefabcdef"
	libOtherVersion = "v0.0.0-20261103000000-012345678901"
)

// libTestModules are the lib/ modules the fake proxy serves, by directory.
var libTestModules = []string{
	"lib/image", "lib/markdown", "lib/password_hashers", "lib/sql/migrate", "lib/sql/postgres", "lib/sql/sqlite", "lib/sql/sqlkit",
}

// fakeLibRelease is a release-shaped nomi with the lib archive's binaries
// beside it, the same nomi alone, and a module proxy serving the lib/
// modules at both versions, with go.sum lines for them.
type fakeLibRelease struct {
	install, bare string
	proxy         string
	sums          map[string]string // version -> go.sum lines of every lib/ module at it
}

var (
	libReleaseOnce  sync.Once
	libReleaseBuilt fakeLibRelease
	libReleaseErr   error
)

func libRelease(t *testing.T) fakeLibRelease {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the lib runners; -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the release environment is a Unix PATH without go (noGoPath)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain to build with")
	}
	root := repoRoot(t)
	libReleaseOnce.Do(func() {
		download := exec.Command("go", "mod", "download")
		download.Dir = filepath.Join(root, "librunners")
		if out, err := download.CombinedOutput(); err != nil {
			libReleaseErr = fmt.Errorf("skip: go mod download in librunners (the lib/ modules' Go dependencies): %v\n%s", err, out)
			return
		}
		base := filepath.Join(filepath.Dir(nomiBin), "librelease")
		b := fakeLibRelease{
			install: filepath.Join(base, "install"),
			bare:    filepath.Join(base, "bare"),
			proxy:   filepath.Join(base, "proxy"),
			sums:    map[string]string{},
		}
		ldflags := "-s -w -X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=" + releaseTestVersion +
			" -X github.com/nomi-language/nomi/internal/ffirun.LibVersion=" + libTestVersion
		steps := []struct{ dir, out, pkg, ldflags string }{
			{root, exe(filepath.Join(b.install, "nomi")), "./cmd/nomi", ldflags},
			{root, exe(filepath.Join(b.bare, "nomi")), "./cmd/nomi", ldflags},
			{filepath.Join(root, "librunners"), exe(filepath.Join(b.install, "nomi-lib")), "./nomi-lib", "-s -w"},
			{filepath.Join(root, "librunners"), exe(filepath.Join(b.install, "nomi-vm-lib")), "./nomi-vm-lib", "-s -w"},
		}
		for _, s := range steps {
			cmd := exec.Command("go", "build", "-trimpath", "-ldflags", s.ldflags, "-o", s.out, s.pkg)
			cmd.Dir = s.dir
			cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
			if out, err := cmd.CombinedOutput(); err != nil {
				libReleaseErr = fmt.Errorf("go build %s: %v\n%s", s.pkg, err, out)
				return
			}
		}
		for _, version := range []string{libTestVersion, libOtherVersion} {
			var sums strings.Builder
			for _, dir := range libTestModules {
				lines, err := publishLibModule(filepath.Join(root, filepath.FromSlash(dir)), b.proxy,
					module.Version{Path: "github.com/nomi-language/nomi/" + dir, Version: version})
				if err != nil {
					libReleaseErr = err
					return
				}
				sums.WriteString(lines)
			}
			b.sums[version] = sums.String()
		}
		libReleaseBuilt = b
	})
	if libReleaseErr != nil {
		if msg, ok := strings.CutPrefix(libReleaseErr.Error(), "skip: "); ok {
			t.Skip(msg)
		}
		t.Fatal(libReleaseErr)
	}
	return libReleaseBuilt
}

// publishLibModule writes the module in dir to the file proxy at proxy as
// m, and answers its go.sum lines.
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
	list, _ := os.ReadFile(filepath.Join(vdir, "list"))
	for name, data := range map[string][]byte{
		"list":              append(list, []byte(m.Version+"\n")...),
		m.Version + ".info": []byte(`{"Version":"` + m.Version + `","Time":"2026-11-04T00:00:00Z"}`),
		m.Version + ".mod":  goMod,
		m.Version + ".zip":  buf.Bytes(),
	} {
		if err := os.WriteFile(filepath.Join(vdir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	zipHash, err := dirhash.HashZip(zipPath, dirhash.Hash1)
	if err != nil {
		return "", err
	}
	modHash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(goMod)), nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s %s %s\n%s %s/go.mod %s\n", m.Path, m.Version, zipHash, m.Path, m.Version, modHash), nil
}

// libProject writes a project at dir that requires every lib/ module at
// version: the sqlkit shop example, with main.nomi also rendering Markdown
// and fitting an image, and a test of each.
func libProject(t *testing.T, b fakeLibRelease, dir, version string, extraGoMod string) {
	t.Helper()
	root := repoRoot(t)
	shop := filepath.Join(root, "lib", "sql", "examples", "shop")
	err := filepath.WalkDir(shop, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(shop, path)
		switch rel {
		case "go.mod", "go.sum", "main.nomi", "README.md":
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
	var gomod strings.Builder
	gomod.WriteString("module shop\n\ngo 1.27.0\n\nrequire (\n")
	for _, m := range libTestModules {
		fmt.Fprintf(&gomod, "\tgithub.com/nomi-language/nomi/%s %s\n", m, version)
	}
	gomod.WriteString(")\n" + extraGoMod)
	mustWrite(t, filepath.Join(dir, "go.mod"), gomod.String())
	mustWrite(t, filepath.Join(dir, "go.sum"), b.sums[version])
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

// libEnv is releaseEnv with the module cache and proxy of the fake release.
func libEnv(t *testing.T, b fakeLibRelease, cacheRoot string, extra ...string) []string {
	t.Helper()
	return releaseEnv(t, cacheRoot, append([]string{
		"GOMODCACHE=" + filepath.Join(t.TempDir(), "modcache"),
		"GOPROXY=" + fileuri.FromPath(b.proxy),
		"GOSUMDB=off",
		"GOFLAGS=-modcacherw",
	}, extra...)...)
}

// TestLibRelease_RunsTestsAndBuildsWithoutGo: a project of sqlite, sqlkit,
// migrate, markdown and image runs, tests and builds on the prebuilt lib
// binaries, with no Go and an empty toolchain cache, and nothing is built.
func TestLibRelease_RunsTestsAndBuildsWithoutGo(t *testing.T) {
	t.Parallel()
	b := libRelease(t)
	cacheRoot := t.TempDir()
	env := libEnv(t, b, cacheRoot)
	proj := t.TempDir()
	libProject(t, b, proj, libTestVersion, "")
	nomi := exe(filepath.Join(b.install, "nomi"))

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
			t.Errorf("the run generated a wrapper in %s", e.Name())
		}
	}
}

// TestLibRelease_ADownloadedLibArchiveRuns: with no nomi-lib beside it, a
// release `nomi` downloads its platform's lib archive from its release,
// checks it against the checksum file, and runs the project.
func TestLibRelease_ADownloadedLibArchiveRuns(t *testing.T) {
	t.Parallel()
	b := libRelease(t)
	wrapper, err := os.ReadFile(exe(filepath.Join(b.install, "nomi-lib")))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := os.ReadFile(exe(filepath.Join(b.install, "nomi-vm-lib")))
	if err != nil {
		t.Fatal(err)
	}
	archive := fmt.Sprintf("nomi_%s_%s_%s_lib.tar.gz", releaseTestVersion, runtime.GOOS, runtime.GOARCH)
	data := tarGzFiles(t, map[string][]byte{"nomi-lib": wrapper, "nomi-vm-lib": runner})
	sums := sha256Hex(data) + "  ./" + archive + "\n"
	var requests sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Store(r.URL.Path, true)
		switch r.URL.Path {
		case "/" + releaseTestVersion + "/" + archive:
			w.Write(data)
		case "/" + releaseTestVersion + "/nomi_" + releaseTestVersion + "_checksums.txt":
			w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cacheRoot := t.TempDir()
	env := libEnv(t, b, cacheRoot, "NOMI_RELEASE_BASE_URL="+srv.URL)
	proj := t.TempDir()
	libProject(t, b, proj, libTestVersion, "")
	nomi := exe(filepath.Join(b.bare, "nomi"))
	for i := 0; i < 2; i++ {
		run := runProcess(t, env, proj, "", nomi, "run", "main.nomi")
		if run.exit != 0 || run.stdout != libProjectWant {
			t.Fatalf("nomi run %d exited %d:\nstdout: %q\nstderr: %s", i, run.exit, run.stdout, run.stderr)
		}
		if i == 0 {
			if _, ok := requests.Load("/" + releaseTestVersion + "/" + archive); !ok {
				t.Fatalf("the first run did not download %s", archive)
			}
			requests.Delete("/" + releaseTestVersion + "/" + archive)
		}
	}
	if _, ok := requests.Load("/" + releaseTestVersion + "/" + archive); ok {
		t.Fatalf("the second run downloaded %s again instead of using the cached one", archive)
	}
	// The download cached the archive's runner too, so a build fetches
	// nothing more.
	out := exe(filepath.Join(t.TempDir(), "shop"))
	build := runProcess(t, env, proj, "", nomi, "build", "main.nomi", "-o", out)
	if build.exit != 0 {
		t.Fatalf("nomi build exited %d:\n%s%s", build.exit, build.stdout, build.stderr)
	}
	if _, ok := requests.Load("/" + releaseTestVersion + "/" + archive); ok {
		t.Fatalf("nomi build downloaded %s again for nomi-vm-lib", archive)
	}
	if built := runProcess(t, env, proj, "", out); built.exit != 0 || built.stdout != libProjectWant {
		t.Fatalf("the built executable exited %d:\nstdout: %q\nstderr: %s", built.exit, built.stdout, built.stderr)
	}
}

// TestLibRelease_WhatNeedsGoIsNamed: a binding of the project's own, or a
// lib/ module at another commit than the release's, takes the wrapper path,
// and with no Go the failure names the binding or the version and how to
// align it.
func TestLibRelease_WhatNeedsGoIsNamed(t *testing.T) {
	t.Parallel()
	b := libRelease(t)
	nomi := exe(filepath.Join(b.install, "nomi"))
	t.Run("a binding of the project's own", func(t *testing.T) {
		env := libEnv(t, b, t.TempDir())
		proj := t.TempDir()
		libProject(t, b, proj, libTestVersion, "")
		mustWrite(t, filepath.Join(proj, "shout", "shout.go"),
			"package shout\n\nimport \"strings\"\n\nfunc Shout(s string) string { return strings.ToUpper(s) }\n")
		mustWrite(t, filepath.Join(proj, "loud.nomi"),
			"gopkg \"shop/shout\" as shout\n\npub fn shout_it(s: String) -> String go shout.Shout\n")
		run := runProcess(t, env, proj, "", nomi, "run", "main.nomi")
		out := run.stdout + run.stderr
		for _, want := range []string{"this project's Go bindings need Go", "loud.nomi:3 binds the Go package shop/shout"} {
			if run.exit == 0 || !strings.Contains(out, want) {
				t.Fatalf("nomi run exited %d; want a failure saying %q:\n%s", run.exit, want, out)
			}
		}
	})
	t.Run("a lib module at another commit", func(t *testing.T) {
		env := libEnv(t, b, t.TempDir())
		proj := t.TempDir()
		libProject(t, b, proj, libOtherVersion, "")
		for _, args := range [][]string{{"run", "main.nomi"}, {"build", "main.nomi", "-o", filepath.Join(t.TempDir(), "shop")}} {
			run := runProcess(t, env, proj, "", nomi, args...)
			out := run.stdout + run.stderr
			for _, want := range []string{
				"this project's Go bindings need Go",
				libOtherVersion + ", and this nomi carries the lib/ modules' Go prebuilt at " + libTestVersion,
				"`nomi update` moves every lib/ module to that version",
			} {
				if run.exit == 0 || !strings.Contains(out, want) {
					t.Fatalf("nomi %s exited %d; want a failure saying %q:\n%s", args[0], run.exit, want, out)
				}
			}
		}
	})
}

// TestLibRelease_ToolOfAModuleFromTheCache: `nomi tool sqlkit generate` runs
// with sqlkit from the module cache, whose go.mod replaces its sibling lib/
// modules with directories the cache does not hold. A Go module's replaces
// apply only when it is the main module, so the tool loads its modules as
// the project's go.mod has them, and it writes the shop example's files as
// committed: on the release's prebuilt lib wrapper with no Go, and in a
// wrapper built with Go for a source build of nomi.
func TestLibRelease_ToolOfAModuleFromTheCache(t *testing.T) {
	t.Parallel()
	b := libRelease(t)
	t.Run("prebuilt, no Go", func(t *testing.T) {
		t.Parallel()
		proj := t.TempDir()
		libProject(t, b, proj, libTestVersion, "")
		checkShopTool(t, libEnv(t, b, t.TempDir()), proj, exe(filepath.Join(b.install, "nomi")))
	})
	t.Run("a wrapper built with Go", func(t *testing.T) {
		t.Parallel()
		goBin, err := exec.LookPath("go")
		if err != nil {
			t.Skip("no go toolchain")
		}
		tmp := t.TempDir()
		// The lib/ modules from the fake proxy, their Go dependencies from
		// this machine's module cache, which libRelease's `go mod download`
		// filled, served as a second file proxy.
		parentModCache := goEnv(t, goBin, "GOMODCACHE")
		env := withUserDirs([]string{
			"HOME=" + filepath.Join(tmp, "home"),
			"PATH=" + os.Getenv("PATH"),
			"TERM=dumb",
			"NOMI_COLOR=never",
			"NOMI_FFIRUN_CACHE_ROOT=" + filepath.Join(tmp, "nomi-cache"),
			"NOMI_TOOLCHAIN_ROOT=" + filepath.Join(tmp, "toolchains"),
			"GOPROXY=" + fileuri.FromPath(b.proxy) + "," + fileuri.FromPath(filepath.Join(parentModCache, "cache", "download")),
			"GOSUMDB=off",
			"GOTOOLCHAIN=local",
			"GOMODCACHE=" + filepath.Join(tmp, "modcache"),
			"GOPATH=" + filepath.Join(tmp, "gopath"),
			"GOCACHE=" + goEnv(t, goBin, "GOCACHE"),
			"GOFLAGS=-modcacherw",
		})
		proj := t.TempDir()
		libProject(t, b, proj, libTestVersion, "")
		checkShopTool(t, env, proj, nomiBin)
	})
}

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
