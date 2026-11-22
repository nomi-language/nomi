package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// A nomi.toml `nomi check` cannot read is reported once, as the manifest's
// error, and nothing else: the program's std imports resolve as they do
// under a good manifest, so std's own files report nothing.
func TestCheck_AManifestErrorIsReportedOnce(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "api.nomi")
	mustWrite(t, entry, `import std::http.{Request, Response, Route}

fn show(Request, user: {id: Int}) -> Response
    _ = user.id
    Response.not_found()
end

fn main()
    _ = Route.to_with("GET /users/{id}", show)
end
`)
	manifest := filepath.Join(dir, "nomi.toml")
	mustWrite(t, manifest, "[module]\nname = \"api\"\nentry_points = [\"api\"]\n")
	if good := runNomiIn(t, dir, "off", "check", entry); good.exit != 0 {
		t.Fatalf("nomi check under a good manifest: %v", good)
	}

	mustWrite(t, manifest, "[package]\nname = \"api\"\n")
	bad := runNomiIn(t, dir, "off", "check", entry)
	if bad.exit == 0 {
		t.Fatalf("nomi check passes under a manifest with a [package] table: %v", bad)
	}
	errors := 0
	for _, line := range strings.Split(bad.stdout+bad.stderr, "\n") {
		if strings.HasPrefix(line, "error") {
			errors++
		}
	}
	if errors != 1 || !strings.Contains(bad.stderr, "nomi.toml:1: nomi.toml has no [package] table") {
		t.Fatalf("nomi check reports %d errors, not the manifest's one:\n%s", errors, bad.stderr)
	}
}
