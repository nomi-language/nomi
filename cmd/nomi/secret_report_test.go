package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The configs of lib/'s drivers that hold a secret show none of it in a
// failed test's report: s3's Credentials hold the secret key and the
// postgres Config the password as std::secret Secrets, which render as
// `Secret(<hidden>)`, and postgres's CancelKey writes `secret: <hidden>`
// for the key's bytes. A project that names both modules by `dir` runs
// tests that fail on a `values:` row, a `returned:` block and `dbg`.
func TestSecretReport_LibConfigsShowNoSecret(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	// Both resolved, since macOS's temporary directory is a link
	// (/var to /private/var) and the manifest's dir is read resolved.
	root, err := filepath.EvalSymlinks(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "app")
	rel := func(parts ...string) string {
		r, err := filepath.Rel(app, filepath.Join(append([]string{root, "lib"}, parts...)...))
		if err != nil {
			t.Skipf("no relative path from the temporary directory to the repository: %v", err)
		}
		return filepath.ToSlash(r)
	}
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"s3 = { dir = \""+rel("storage", "s3")+"\" }\n"+
		"postgres = { dir = \""+rel("sql", "postgres")+"\" }\n")
	mustWrite(t, filepath.Join(app, "secrets_test.nomi"), `import
    std::secret.Secret
    std::url.Url
    postgres::config
    postgres::wire.CancelKey
    s3.{Config, Credentials}
end

fn bucket() -> Config
    Config{
        endpoint:    Url"http://localhost:9000",
        bucket:      "photos",
        credentials: Credentials{access_key: "minio", secret_key: Secret.new("s3-key-text")},
    }
end

fn database() -> config.Config
    config.parse("postgres://ann:pg-password-text@db/shop")!
end

fn key() -> CancelKey
    CancelKey{config: database(), tls: False, process: 7, secret: Bytes.from_list([222, 173])}
end

fn fail<E>(e: E) -> Result<Int, E>
    Err(e)
end

test "values"
    dbg bucket()
    dbg database()
    assert [bucket()] == [] or [database()] == [] or [key()] == []
end

test "returned"
    _ = fail((bucket(), database(), key()))?
    assert True
end
`)
	run := runProcess(t, append(buildEnv(t.TempDir()), artifactCacheEnv(t.TempDir())...), app, "", nomiBin, "test", ".")
	out := run.stdout + run.stderr
	if run.exit != 1 {
		t.Fatalf("nomi test exited %d, want 1 for two failed tests:\n%s", run.exit, out)
	}
	for _, want := range []string{
		"secret_key: Secret(<hidden>)",
		"password: Some(Secret(<hidden>))",
		"secret: <hidden>}",
		"returned:",
		"0 passed, 2 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	// The report quotes source lines, which name the texts; no rendered
	// value does, and no Bytes of the cancel key reach it.
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		rendered := strings.HasPrefix(trimmed, "= ") || strings.HasPrefix(trimmed, "Err(") || strings.HasPrefix(trimmed, "dbg ")
		if rendered && (strings.Contains(line, "s3-key-text") || strings.Contains(line, "pg-password-text") || strings.Contains(line, "<<")) {
			t.Errorf("a rendered value shows a secret: %q", line)
		}
	}
}
